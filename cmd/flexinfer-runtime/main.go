/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	_ "github.com/flexinfer/flexinfer/backend" // register all backends
	"github.com/flexinfer/flexinfer/internal/runtime"
	"github.com/flexinfer/flexinfer/pkg/envutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	var (
		listenAddr      string
		gpuVendor       string
		gpuArch         string
		modelBasePath   string
		shutdownTimeout time.Duration
		healthInterval  time.Duration
	)

	flag.StringVar(&listenAddr, "listen", ":8080", "Address for the runtime API server.")
	flag.StringVar(&gpuVendor, "gpu-vendor", envutil.StringOrDefault("GPU_VENDOR", "cpu"), "GPU vendor: amd, nvidia, cpu.")
	flag.StringVar(&gpuArch, "gpu-arch", envutil.StringOrDefault("GPU_ARCH", ""), "GPU architecture: gfx1100, gfx906, sm_52, etc.")
	flag.StringVar(&modelBasePath, "model-base-path", "/models", "Root path where models are mounted.")
	flag.DurationVar(&shutdownTimeout, "shutdown-timeout", 30*time.Second, "Time to wait for subprocess graceful exit.")
	flag.DurationVar(&healthInterval, "health-interval", 5*time.Second, "Health check polling interval.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := log.Log.WithName("flexinfer-runtime")

	// Validate backend registration.
	if err := backend.RegistrationError(); err != nil {
		logger.Error(err, "Backend registration failed")
		os.Exit(1)
	}

	// Register Prometheus metrics for the runtime.
	runtime.RegisterMetrics()

	// Set runtime info gauge.
	nodeName := envutil.StringOrDefault("NODE_NAME", "")
	if nodeName == "" {
		nodeName, _ = os.Hostname()
	}
	runtimeProfile := runtime.RuntimeProfileLabel(envutil.StringOrDefault("RUNTIME_PROFILE", ""))
	runtimeDigest := runtime.RuntimeDigestLabel(envutil.StringOrDefault("RUNTIME_IMAGE", ""))
	runtime.RuntimeInfo.WithLabelValues(nodeName, gpuVendor, gpuArch, runtimeProfile, runtimeDigest).Set(1)

	logger.Info("Starting flexinfer-runtime",
		"listenAddr", listenAddr,
		"gpuVendor", gpuVendor,
		"gpuArch", gpuArch,
		"runtimeProfile", runtimeProfile,
		"runtimeDigest", runtimeDigest,
		"modelBasePath", modelBasePath,
		"backends", backend.List(),
	)

	startTime := time.Now()

	// Background goroutine to update uptime and GPU metrics.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			runtime.RuntimeUptimeSeconds.Set(time.Since(startTime).Seconds())

			gpuInfo := runtime.QueryGPU(gpuVendor, gpuArch)
			runtime.GPUVRAMTotalBytesRT.WithLabelValues(gpuVendor, gpuArch).Set(float64(gpuInfo.VRAMTotalMB) * 1024 * 1024)
			runtime.GPUVRAMUsedBytesRT.WithLabelValues(gpuVendor, gpuArch).Set(float64(gpuInfo.VRAMUsedMB) * 1024 * 1024)
			runtime.GPUVRAMFreeBytesRT.WithLabelValues(gpuVendor, gpuArch).Set(float64(gpuInfo.VRAMFreeMB) * 1024 * 1024)
			runtime.GPUTemperatureCelsiusRT.WithLabelValues(gpuVendor, gpuArch).Set(gpuInfo.Temperature)

			<-ticker.C
		}
	}()

	mgr := runtime.NewManager(runtime.ManagerConfig{
		GPUVendor:           backend.GPUVendor(gpuVendor),
		GPUArch:             gpuArch,
		ShutdownTimeout:     shutdownTimeout,
		HealthCheckInterval: healthInterval,
		ModelBasePath:       modelBasePath,
	})

	srv := runtime.NewServer(mgr)

	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start HTTP server in background.
	go func() {
		logger.Info("API server listening", "addr", listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(err, "API server error")
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	logger.Info("Received shutdown signal")

	// Shutdown sequence: stop accepting requests, unload model, close server.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout+10*time.Second)
	defer cancel()

	logger.Info("Unloading active model")
	if err := mgr.Shutdown(shutdownCtx); err != nil {
		logger.Error(err, "Error during model unload")
	}

	logger.Info("Stopping HTTP server")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error(err, "Error during HTTP server shutdown")
	}

	fmt.Println("flexinfer-runtime stopped")
}
