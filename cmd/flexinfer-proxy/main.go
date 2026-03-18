package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strings"

	"github.com/flexinfer/flexinfer/internal/proxy"
	"github.com/flexinfer/flexinfer/pkg/observability"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	var port int
	var logLevel string
	flag.IntVar(&port, "port", 8080, "Port to listen on")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.Parse()

	// Initialize structured logging
	var level slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	ctrl.SetLogger(zap.New(zap.UseDevMode(level == slog.LevelDebug)))

	shutdownTracing, err := observability.InitTracing(context.Background(), "flexinfer-proxy")
	if err != nil {
		slog.Error("unable to initialize tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := shutdownTracing(context.Background()); err != nil {
			slog.Error("failed to shutdown tracing", "error", err)
		}
	}()

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
		slog.Warn("POD_NAMESPACE not set, using 'default' namespace (set POD_NAMESPACE env var in production)")
	}

	cfg, err := config.GetConfig()
	if err != nil {
		slog.Error("unable to get kubeconfig", "error", err)
		os.Exit(1)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: proxy.Scheme})
	if err != nil {
		slog.Error("unable to create k8s client", "error", err)
		os.Exit(1)
	}

	proxyCfg := proxy.ConfigFromEnv(k8sClient, namespace)
	if err := proxyCfg.Validate(); err != nil {
		slog.Error("invalid proxy configuration", "error", err)
		os.Exit(1)
	}
	p := proxy.New(proxyCfg)
	if err := p.Run(port); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
