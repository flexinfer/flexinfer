package daemon

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"time"

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
		return nil // HTTP listener not configured
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

	// Start listener
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		var err error
		if server.TLSConfig != nil {
			d.logger.Info("HTTP+TLS listener started", "addr", addr)
			err = server.ListenAndServeTLS("", "")
		} else {
			d.logger.Info("HTTP listener started", "addr", addr)
			err = server.ListenAndServe()
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
		}
	}
}
