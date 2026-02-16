package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/gateway"
	"gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/gateway/auth"

	"github.com/crb2nu/loom/pkg/secrets"
)

// authContextKey is the context key for auth context.
type authContextKey struct{}

// AuthContextFromRequest extracts the auth context from an HTTP request.
func AuthContextFromRequest(r *http.Request) *auth.AuthContext {
	val := r.Context().Value(authContextKey{})
	if val == nil {
		return nil
	}
	return val.(*auth.AuthContext)
}

// buildAuthenticator creates a gateway.Authenticator from the daemon's HTTP auth config.
func (d *Daemon) buildAuthenticator() (gateway.Authenticator, error) {
	cfg := d.fileCfg.HTTP.Auth

	switch cfg.Type {
	case "", "none":
		return nil, nil

	case "token":
		token, err := d.resolveToken(cfg.TokenSecretKey)
		if err != nil {
			return nil, fmt.Errorf("resolve auth token: %w", err)
		}
		return &gateway.TokenAuthenticator{Token: token}, nil

	case "oidc":
		if cfg.OIDCIssuer == "" || cfg.OIDCClientID == "" {
			return nil, fmt.Errorf("OIDC auth requires oidc_issuer and oidc_client_id")
		}
		oidcAuth := gateway.NewOIDCAuthenticator(cfg.OIDCIssuer, cfg.OIDCClientID)
		if err := oidcAuth.Initialize(context.Background()); err != nil {
			return nil, fmt.Errorf("initialize OIDC: %w", err)
		}
		return oidcAuth, nil

	case "mtls":
		return &gateway.CertAuthenticator{
			AllowedCommonNames: cfg.AllowedCommonNames,
		}, nil

	default:
		return nil, fmt.Errorf("unknown auth type: %q", cfg.Type)
	}
}

// buildAuthHook creates an auth.Hook from the authenticator.
func (d *Daemon) buildAuthHook(authenticator gateway.Authenticator) auth.Hook {
	if authenticator == nil {
		return &auth.NoOpHook{}
	}
	return &authenticatorHook{authenticator: authenticator}
}

// authenticatorHook adapts a gateway.Authenticator to the auth.Hook interface.
type authenticatorHook struct {
	authenticator gateway.Authenticator
}

func (h *authenticatorHook) OnConnect(ctx context.Context, r *http.Request) (*auth.AuthContext, error) {
	if err := h.authenticator.Authenticate(r); err != nil {
		return nil, err
	}
	return &auth.AuthContext{
		Subject: "authenticated",
	}, nil
}

func (h *authenticatorHook) OnMessage(_ context.Context, _ *auth.AuthContext, _ []byte) error {
	return nil
}

// buildAuthMiddleware creates HTTP middleware that enforces authentication.
func (d *Daemon) buildAuthMiddleware() (func(http.Handler) http.Handler, error) {
	addr := d.cfg.HTTPAddr
	host, _, _ := net.SplitHostPort(addr)

	// Localhost binding: auth not required
	isLocalhost := host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1"

	if isLocalhost && d.fileCfg.HTTP.Auth.Type == "" {
		d.logger.Info("HTTP listener on localhost, auth disabled")
		return nil, nil
	}

	// Non-localhost binding: auth required
	if !isLocalhost && d.fileCfg.HTTP.Auth.Type == "" {
		return nil, fmt.Errorf("HTTP listener bound to %s requires auth configuration (set http.auth.type in config.yaml)", addr)
	}

	authenticator, err := d.buildAuthenticator()
	if err != nil {
		return nil, err
	}

	if authenticator == nil {
		return nil, nil
	}

	hook := d.buildAuthHook(authenticator)

	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCtx, err := hook.OnConnect(r.Context(), r)
			if err != nil {
				d.logger.Warn("HTTP auth failed",
					slog.String("remote", r.RemoteAddr),
					slog.String("error", err.Error()),
				)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Attach auth context to request
			ctx := context.WithValue(r.Context(), authContextKey{}, authCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	d.logger.Info("HTTP auth enabled", "type", d.fileCfg.HTTP.Auth.Type)
	return middleware, nil
}

// resolveToken retrieves a bearer token from the secret store.
func (d *Daemon) resolveToken(secretKey string) (string, error) {
	if secretKey == "" {
		secretKey = "LOOM_HTTP_TOKEN"
	}

	mgr, err := secrets.DefaultManager()
	if err != nil {
		return "", fmt.Errorf("init secrets manager: %w", err)
	}

	value, _, err := mgr.Get(secretKey)
	if err != nil {
		return "", fmt.Errorf("secret %q not found (set with: loom secrets set %s <token>)", secretKey, secretKey)
	}
	return value, nil
}

// initAuth builds and attaches the auth middleware to the daemon.
// Called during startHTTPListener before the HTTP server is started.
func (d *Daemon) initAuth() error {
	middleware, err := d.buildAuthMiddleware()
	if err != nil {
		return err
	}
	d.authMiddleware = middleware
	return nil
}
