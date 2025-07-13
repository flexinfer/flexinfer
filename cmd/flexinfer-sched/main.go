package main

import (
	"flag"
	"net/http"

	"github.com/flexinfer/flexinfer/scheduler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
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

	setupLog.Info("Scheduler listening on :8888")
	if err := http.ListenAndServe(":8888", nil); err != nil {
		setupLog.Error(err, "Failed to start HTTP server")
	}
}
