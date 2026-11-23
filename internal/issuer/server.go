package issuer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexliesenfeld/health"
	"github.com/gin-contrib/expvar"
	"github.com/gin-contrib/graceful"
	"github.com/gin-contrib/pprof"
	"github.com/gin-contrib/requestid"
	"github.com/gin-contrib/slog"
	"github.com/gin-contrib/timeout"
	"github.com/gin-gonic/gin"

	"crusoe-registry-token-issuer/internal/config"
	"crusoe-registry-token-issuer/internal/crusoe"
	"crusoe-registry-token-issuer/internal/logging"
)

func Serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger, err := logging.Logger(cfg.Issuer.Log)
	if err != nil {
		return err
	}
	logging.SetDefault(logger)
	logging.LogBuildInformation()

	if cfg.Issuer.Server.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	client, err := crusoe.NewClient(cfg, logger)
	if err != nil {
		return err
	}

	startupCtx, cancel := context.WithTimeoutCause(
		context.Background(),
		cfg.Issuer.Server.Timeout.Request,
		errors.New("timeout exceeded"),
	)
	email, err := client.GetUserEmail(startupCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("verifying crusoe credentials: %w", err)
	}

	engine := gin.New()
	engine.Use(
		gin.CustomRecoveryWithWriter(io.Discard, recoverFromPanic),
	)
	engine.Use(
		requestid.New(
			requestid.WithCustomHeaderStrKey(xRequestID),
		),
	)
	engine.Use(
		slog.SetLogger(
			slog.WithUTC(true),
			slog.WithRequestHeader(cfg.Issuer.Server.Debug),
			slog.WithLogger(requestLogger),
		),
	)
	engine.Use(
		timeout.New(
			timeout.WithTimeout(cfg.Issuer.Server.Timeout.Request),
			timeout.WithResponse(requestTimeout),
		),
	)

	// TODO: Expose setting for trusted proxies: https://gin-gonic.com/en/docs/server-config/trusted-proxies
	if err := engine.SetTrustedProxies(nil); err != nil {
		return err
	}

	check := health.Check{
		Name:               "crusoe-api",
		MaxContiguousFails: 2,
		Check: func(ctx context.Context) error {
			_, err := client.GetUserEmail(ctx)
			return err
		},
	}
	checker := health.NewChecker(
		health.WithTimeout(cfg.Issuer.Server.Timeout.Request),
		health.WithPeriodicCheck(time.Minute*5, 0, check),
		health.WithStatusListener(func(_ context.Context, state health.CheckerState) {
			for key, value := range state.CheckState {
				logger.Info("upstream service state changed", "service", key, "state", value.Status)
			}
		}),
	)

	api := engine.Group(cfg.Issuer.Server.BasePath)
	{
		// TODO: Endpoint for metrics based on: https://gin-gonic.com/en/docs/server-config/metrics

		debug := api.Group("/debug", func(ctx *gin.Context) {
			if !cfg.Issuer.Server.Debug {
				ctx.AbortWithStatus(http.StatusNotFound)
				return
			}
		})

		debug.GET("/var", expvar.Handler())

		pprof.Register(debug, "pprof")

		api.POST("/token", func(ctx *gin.Context) {
			token, err := client.CreateToken(ctx.Request.Context())
			if ctx.Request.Context().Err() != nil {
				return // timeout middleware already wrote the response
			}

			body := gin.H{}
			if err != nil {
				logger.Error("failed to issue token", "error", err, "request_id", requestid.Get(ctx))
				status := http.StatusBadGateway
				err, ok := errors.AsType[*crusoe.APIError](err)
				if ok && err.StatusCode == http.StatusTooManyRequests {
					status = http.StatusTooManyRequests
				}
				body["error"] = http.StatusText(status)
				ctx.JSON(status, body)
				return
			}

			body["username"] = email
			body["password"] = token
			body["registry"] = cfg.Issuer.Registry

			ctx.Header("Cache-Control", "no-store")
			ctx.JSON(http.StatusOK, body)
		})

		api.GET("/readyz", gin.WrapH(health.NewHandler(checker)))

		api.GET("/healthz", func(ctx *gin.Context) {
			body := gin.H{"status": "up"}
			ctx.JSON(http.StatusOK, body)
		})

		api.GET("/", func(ctx *gin.Context) {
			ctx.Redirect(http.StatusFound, api.BasePath()+"/healthz")
		})
	}

	engine.GET("/", func(ctx *gin.Context) {
		ctx.Redirect(http.StatusFound, api.BasePath())
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server, err := graceful.New(engine,
		graceful.WithAddr(cfg.Issuer.Server.Address()),
		graceful.WithServerTimeouts(0, cfg.Issuer.Server.Timeout.Request+500*time.Millisecond, 0),
		graceful.WithShutdownTimeout(cfg.Issuer.Server.Timeout.Shutdown),
		graceful.WithBeforeShutdown(func(_ context.Context) error {
			checker.Stop()
			stop()
			return nil
		}),
	)
	if err != nil {
		return err
	}
	defer server.Close()

	if err := server.RunWithContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}
