package issuer

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

const xRequestID = "X-Request-ID"

func recoverFromPanic(ctx *gin.Context, err any) {
	slog.Default().Error("panic", "error", err)
	ctx.AbortWithStatus(http.StatusInternalServerError)
}

func requestLogger(ctx *gin.Context, _ *slog.Logger) *slog.Logger {
	return slog.Default().With(
		slog.String("request_id", ctx.GetHeader(xRequestID)),
	)
}

func requestTimeout(ctx *gin.Context) {
	ctx.JSON(
		http.StatusRequestTimeout,
		gin.H{"error": "timeout"},
	)
}
