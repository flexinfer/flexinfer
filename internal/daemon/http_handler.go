package daemon

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/crb2nu/loom/internal/hud"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// newStreamableHTTPHandler creates an http.Handler that bridges Streamable HTTP to daemon dispatch.
func (d *Daemon) newStreamableHTTPHandler() *mcp.StreamableHTTPServer {
	cfg := mcp.DefaultStreamableHTTPConfig()

	// Apply config from file
	if d.fileCfg.HTTP.SessionTimeoutMinutes > 0 {
		cfg.SessionTimeout = time.Duration(d.fileCfg.HTTP.SessionTimeoutMinutes) * time.Minute
	}
	if d.fileCfg.HTTP.MaxSessions > 0 {
		cfg.MaxSessions = d.fileCfg.HTTP.MaxSessions
	}
	if len(d.fileCfg.HTTP.AllowedOrigins) > 0 {
		cfg.AllowedOrigins = d.fileCfg.HTTP.AllowedOrigins
	}
	cfg.SessionRequired = true

	handler := func(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
		return d.handleMessage(ctx, msg)
	}

	srv := mcp.NewStreamableHTTPServer(handler, cfg)
	srv.SetLogger(func(msg string, args ...any) {
		d.logger.Info(msg, args...)
	})
	return srv
}

// startHTTPListener starts the Streamable HTTP listener alongside the Unix socket.
func (d *Daemon) startHTTPListener(ctx context.Context) error {
	addr := d.cfg.HTTPAddr
	if addr == "" {
		if !d.fileCfg.EmbeddedHUD.Enabled {
			return nil // Neither HTTP listener nor embedded HUD configured
		}
		addr = "localhost:0" // Auto-assign port for embedded HUD
	}

	// Initialize auth before creating the handler
	if err := d.initAuth(); err != nil {
		return err
	}

	handler := d.newStreamableHTTPHandler()
	d.httpStreamable = handler

	mux := http.NewServeMux()

	// Auth middleware wraps the MCP endpoint (Phase 3 will add real auth)
	var mcpHandler http.Handler = handler
	if d.authMiddleware != nil {
		mcpHandler = d.authMiddleware(handler)
	}
	mux.Handle("/mcp", mcpHandler)

	// Health endpoint (unauthenticated, useful for LB probes)
	mux.HandleFunc("/health", d.HealthHandler())

	// OAuth 2.1 endpoints (unauthenticated, part of the OAuth flow itself)
	if d.oauth != nil {
		mux.HandleFunc("/.well-known/oauth-authorization-server", d.oauth.HandleMetadata)
		mux.HandleFunc("/.well-known/oauth-protected-resource", d.oauth.HandleResourceMetadata)
		mux.HandleFunc("/oauth2/register", d.oauth.HandleRegister)
		mux.HandleFunc("/oauth2/authorize", d.oauth.HandleAuthorize)
		mux.HandleFunc("/oauth2/token", d.oauth.HandleToken)
		mux.HandleFunc("/oauth2/revoke", d.oauth.HandleRevoke)
	}

	// Embedded HUD: mount dashboard, mobile API, and SSE routes on the same mux.
	if d.fileCfg.EmbeddedHUD.Enabled {
		if err := d.startEmbeddedHUD(ctx, mux); err != nil {
			d.logger.Error("embedded HUD init failed, continuing without HUD", "error", err)
		}
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// TLS configuration
	if d.fileCfg.HTTP.TLSCertFile != "" && d.fileCfg.HTTP.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(d.fileCfg.HTTP.TLSCertFile, d.fileCfg.HTTP.TLSKeyFile)
		if err != nil {
			return err
		}
		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		}
	}

	// Warn if binding to non-localhost without TLS
	host, _, _ := net.SplitHostPort(addr)
	if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "" {
		if server.TLSConfig == nil {
			d.logger.Warn("HTTP listener bound to non-localhost without TLS",
				"addr", addr,
				slog.String("recommendation", "configure tls_cert_file and tls_key_file"))
		}
	}

	d.httpServer = server

	// Start session reaper
	go d.httpSessionReaperLoop()

	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	if d.fileCfg.EmbeddedHUD.Enabled {
		writeEmbeddedHUDPortFile(d.logger, listener.Addr())
	}

	// Start listener
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer listener.Close()
		defer func() {
			if d.fileCfg.EmbeddedHUD.Enabled {
				if err := hud.RemovePortFile(); err != nil {
					d.logger.Warn("failed to remove embedded HUD port file", "error", err)
				}
			}
		}()
		var err error
		if server.TLSConfig != nil {
			d.logger.Info("HTTP+TLS listener started", "addr", listener.Addr().String())
			err = server.Serve(tls.NewListener(listener, server.TLSConfig))
		} else {
			d.logger.Info("HTTP listener started", "addr", listener.Addr().String())
			err = server.Serve(listener)
		}
		if err != nil && err != http.ErrServerClosed {
			d.logger.Error("HTTP listener error", "error", err)
		}
	}()

	// Shutdown on context cancel
	go func() {
		select {
		case <-ctx.Done():
		case <-d.done:
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	return nil
}

func writeEmbeddedHUDPortFile(logger *slog.Logger, addr net.Addr) {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return
	}
	portFile, err := hud.WritePortFile(tcpAddr.Port)
	if err != nil {
		logger.Warn("failed to write embedded HUD port file", "path", portFile, "error", err)
		return
	}
	logger.Info("embedded HUD port file written", "path", portFile, "port", tcpAddr.Port)
}

// httpSessionReaperLoop periodically cleans up expired HTTP sessions.
func (d *Daemon) httpSessionReaperLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if d.httpStreamable != nil {
				reaped := d.httpStreamable.ReapExpiredSessions()
				if reaped > 0 {
					d.logger.Info("reaped expired HTTP sessions", "count", reaped)
				}
			}
			if d.oauth != nil {
				reaped := d.oauth.ReapExpired()
				if reaped > 0 {
					d.logger.Info("reaped expired OAuth codes/tokens", "count", reaped)
				}
			}
		}
	}
}
