package main

import (
	"fmt"
	"flag"
	"net/http"

	"github.com/flexinfer/flexinfer/scheduler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	var port int
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.IntVar(&port, "port", 8082, "HTTP port to listen on for the scheduler extender.")
	flag.Parse()

	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := log.Log.WithName("setup")

	setupLog.Info("Starting flexinfer-sched...")

	sched, err := scheduler.NewScheduler()
	if err != nil {
		setupLog.Error(err, "Failed to create scheduler")
	}

	http.HandleFunc("/filter", sched.Filter)
	http.HandleFunc("/score", sched.Score)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	addr := fmt.Sprintf(":%d", port)
	setupLog.Info("Scheduler listening", "addr", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		setupLog.Error(err, "Failed to start HTTP server")
	}
}
