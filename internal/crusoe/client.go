package crusoe

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/antihax/optional"
	"github.com/crusoecloud/client-go/auth/v1"
	"github.com/crusoecloud/client-go/swagger/v1"
	"github.com/hashicorp/go-retryablehttp"

	"crusoe-registry-token-issuer/internal"
	"crusoe-registry-token-issuer/internal/config"
)

type APIError struct {
	Body       []byte
	StatusCode int
}

func (apiError *APIError) Error() string {
	return fmt.Sprintf(
		"crusoe api: %d %s: %s",
		apiError.StatusCode,
		http.StatusText(apiError.StatusCode),
		bytes.TrimSpace(apiError.Body),
	)
}

type Client interface {
	GetUserEmail(ctx context.Context) (string, error)
	CreateToken(ctx context.Context) (string, error)
}

type client struct {
	tokenPrefix string
	tokenTTL    time.Duration
	logger      *slog.Logger
	apiClient   *swagger.APIClient
}

func NewClient(
	cfg *config.Crusoe,
	logger *slog.Logger,
) (Client, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	logger = cmp.Or(logger, slog.Default())

	retryableHTTPClient := retryablehttp.NewClient()
	retryableHTTPClient.Logger = logger
	retryableHTTPClient.RetryMax = 4
	retryableHTTPClient.CheckRetry = checkRetry
	retryableHTTPClient.ErrorHandler = retryablehttp.PassthroughErrorHandler
	retryableHTTPClient.HTTPClient.Transport = auth.NewAuthenticatingTransport(
		retryableHTTPClient.HTTPClient.Transport,
		string(cfg.AccessKey),
		string(cfg.SecretKey),
	)

	apiClientConfig := swagger.NewConfiguration()
	apiClientConfig.UserAgent = internal.Project + "/" + internal.Version
	apiClientConfig.HTTPClient = retryableHTTPClient.StandardClient()

	return &client{
		tokenPrefix: cfg.Issuer.Token.Prefix,
		tokenTTL:    cfg.Issuer.Token.TTL,
		logger:      logger,
		apiClient:   swagger.NewAPIClient(apiClientConfig),
	}, nil
}

func (client *client) GetUserEmail(ctx context.Context) (string, error) {
	user, response, err := client.apiClient.IdentitiesApi.GetUserIdentity(ctx)
	if response != nil {
		defer client.cleanUpResponseBody(response.Body)
	}
	if err != nil {
		return "", wrapError(response, err)
	}
	return user.Identity.Email, nil
}

func (client *client) CreateToken(ctx context.Context) (string, error) {
	expiry := time.Now().UTC().Add(client.tokenTTL)
	alias := fmt.Sprintf("%s%s-%s", client.tokenPrefix, expiry.Format("20060102150405"), rand.Text())
	expiresAt := expiry.Format(time.RFC3339)
	body := swagger.CcrTokenRequest{
		Alias:     alias,
		ExpiresAt: expiresAt,
	}
	opts := swagger.CcrApiCreateCcrTokenOpts{
		Body: optional.NewInterface(body),
	}
	result, response, err := client.apiClient.CcrApi.CreateCcrToken(ctx, &opts)
	if response != nil {
		defer client.cleanUpResponseBody(response.Body)
	}
	if err != nil {
		return "", wrapError(response, err)
	}
	return result.Token, nil
}

func (client *client) cleanUpResponseBody(closer io.Closer) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		client.logger.Error("failed to close response body", "error", err)
	}
}

func checkRetry(ctx context.Context, response *http.Response, err error) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return retryablehttp.DefaultRetryPolicy(ctx, response, err)
	}
	return response.StatusCode == http.StatusTooManyRequests, nil
}

func wrapError(response *http.Response, err error) error {
	swaggerError, ok := errors.AsType[swagger.GenericSwaggerError](err)
	if !ok || response == nil {
		return err
	}
	return &APIError{
		Body:       swaggerError.Body(),
		StatusCode: response.StatusCode,
	}
}
