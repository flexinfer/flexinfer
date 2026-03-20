package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Server is the HTTP API server for the flexinfer-runtime.
type Server struct {
	manager *Manager
	mux     *http.ServeMux
}

// NewServer creates an HTTP API server backed by the given manager.
func NewServer(m *Manager) *Server {
	s := &Server{manager: m}
	s.mux = http.NewServeMux()
	s.registerRoutes()
	return s
}

// Handler returns the http.Handler for the server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/models", s.handleListModels)
	s.mux.HandleFunc("POST /api/v1/models/{name}/load", s.handleLoadModel)
	s.mux.HandleFunc("DELETE /api/v1/models/{name}", s.handleUnloadModel)
	s.mux.HandleFunc("GET /api/v1/models/{name}/health", s.handleModelHealth)
	s.mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/v1/mode", s.handleGetMode)
	s.mux.HandleFunc("PUT /api/v1/mode", s.handleSetMode)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.Handle("GET /metrics", promhttp.Handler())
}

// handleListModels returns the currently loaded model (if any).
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	active := s.manager.Active()
	var models []ModelSummary
	if active != nil {
		models = append(models, ModelSummary{
			Name:     active.Name,
			Backend:  active.Backend,
			Model:    active.Model,
			State:    string(active.State),
			Port:     active.Port,
			PID:      active.PID,
			LoadedAt: active.LoadedAt,
			Error:    active.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"models": models,
	})
}

// handleLoadModel starts a backend subprocess for the given model.
func (s *Server) handleLoadModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "model name is required")
		return
	}

	var req LoadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	if req.Backend == "" {
		writeError(w, http.StatusBadRequest, "backend is required")
		return
	}

	logger := log.FromContext(r.Context()).WithValues("model", name)
	logger.Info("Loading model", "backend", req.Backend, "source", req.Model)

	if err := s.manager.Load(r.Context(), name, req); err != nil {
		logger.Error(err, "Failed to load model")
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "loading",
		"model":  name,
	})
}

// handleUnloadModel stops the backend subprocess for the given model.
func (s *Server) handleUnloadModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "model name is required")
		return
	}

	if err := s.manager.Unload(r.Context(), name); err != nil {
		if strings.Contains(err.Error(), "not loaded") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "unloaded",
		"model":  name,
	})
}

// handleModelHealth returns the health of a specific model.
func (s *Server) handleModelHealth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	active := s.manager.Active()

	if active == nil || active.Name != name {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q is not loaded", name))
		return
	}

	status := http.StatusOK
	if active.State != ModelStateReady {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, map[string]interface{}{
		"name":  active.Name,
		"state": string(active.State),
		"error": active.Error,
	})
}

// handleStatus returns the overall runtime status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := s.manager.Status()

	gpuInfo := QueryGPU(status.GPUVendor, status.GPUArch)
	resp := map[string]interface{}{
		"gpuVendor": status.GPUVendor,
		"gpuArch":   status.GPUArch,
		"gpu":       gpuInfo,
	}
	if status.ActiveModel != nil {
		resp["activeModel"] = status.ActiveModel
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleHealthz is the liveness probe — always returns 200 if the process is running.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz returns 200 if the GPU device is accessible.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	status := s.manager.Status()
	if !GPUDeviceAccessible(status.GPUVendor) {
		writeError(w, http.StatusServiceUnavailable, "GPU device not accessible")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// handleGetMode returns the current node mode ("inference" or "gaming").
func (s *Server) handleGetMode(w http.ResponseWriter, r *http.Request) {
	mode := s.manager.Mode()
	writeJSON(w, http.StatusOK, map[string]string{"mode": string(mode)})
}

// handleSetMode switches the node between inference and gaming mode.
// PUT /api/v1/mode {"mode": "gaming"} — unloads current model, loads steam backend
// PUT /api/v1/mode {"mode": "inference"} — unloads steam, makes node available
func (s *Server) handleSetMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	target := NodeMode(req.Mode)
	if target != ModeInference && target != ModeGaming {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid mode %q: must be %q or %q", req.Mode, ModeInference, ModeGaming))
		return
	}

	logger := log.FromContext(r.Context())
	logger.Info("Mode switch requested", "target", target, "current", s.manager.Mode())

	if err := s.manager.SetMode(r.Context(), target); err != nil {
		logger.Error(err, "Failed to switch mode")
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"mode":   string(target),
		"status": "ok",
	})
}
