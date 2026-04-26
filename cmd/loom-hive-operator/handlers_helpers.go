package main

import (
	"encoding/json"
	"net/http"
)

// writeJSON is the canonical JSON writer for every operator handler.
// Sets the content type, encodes with default options, and silently
// swallows write errors — the caller has already disconnected if
// Encode fails, and noise in the logs from a closed conn isn't
// actionable.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// notImplemented is the standard 501 response for endpoints whose
// happy-path implementation lands in a later slice. The body names the
// slice that will fill it in so callers can grep for it in the plan.
func notImplemented(w http.ResponseWriter, slice string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "not implemented",
		"slice": slice,
	})
}
