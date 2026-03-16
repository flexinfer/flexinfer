// Package runtime provides shared types and utilities for communicating
// with flexinfer-runtime pods. Used by both the proxy (direct load path)
// and the controller (reconcile path).
package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LoadPayload is the JSON body sent to POST /api/v1/models/{name}/load.
// This matches the runtime.LoadRequest struct accepted by the runtime API.
type LoadPayload struct {
	Backend   string                 `json:"backend"`
	Model     string                 `json:"model"`
	ModelPath string                 `json:"modelPath,omitempty"`
	Config    map[string]interface{} `json:"config,omitempty"`
}

// BuildLoadPayload constructs a serialized LoadPayload from model metadata.
// Both the proxy and controller use this to build identical load requests.
// The modelBasePath is the runtime's model mount root (e.g. "/models").
func BuildLoadPayload(backendName, source, modelBasePath string, config map[string]interface{}) ([]byte, error) {
	model := ExtractModelFromSource(source)
	payload := LoadPayload{
		Backend: backendName,
		Model:   model,
		Config:  config,
	}
	// For pvc:// sources, resolve the full modelPath so the runtime doesn't
	// fall back to its default (basePath/modelName) which may be wrong.
	// The PVC is mounted at modelBasePath/<pvc-name> (e.g. /models/my-pvc),
	// so include the PVC name in the path before the subpath.
	if strings.HasPrefix(source, "pvc://") && modelBasePath != "" {
		pvcParts := strings.SplitN(strings.TrimPrefix(source, "pvc://"), "/", 2)
		if len(pvcParts) >= 1 {
			pvcName := pvcParts[0]
			payload.ModelPath = modelBasePath + "/" + pvcName + model
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling load payload: %w", err)
	}
	return data, nil
}

// ExtractModelFromSource strips the scheme prefix from a model source URI.
//
//	HF://org/model     -> org/model
//	ollama://model:tag -> model:tag
//	file:///path       -> /path
//	pvc://name/path    -> /path
func ExtractModelFromSource(source string) string {
	if strings.HasPrefix(source, "HF://") {
		return strings.TrimPrefix(source, "HF://")
	}
	if strings.HasPrefix(source, "ollama://") {
		return strings.TrimPrefix(source, "ollama://")
	}
	if strings.HasPrefix(source, "file://") {
		return strings.TrimPrefix(source, "file://")
	}
	if strings.HasPrefix(source, "pvc://") {
		parts := strings.SplitN(strings.TrimPrefix(source, "pvc://"), "/", 2)
		if len(parts) == 2 {
			return "/" + parts[1]
		}
	}
	return source
}

// RuntimeEndpoint represents a discovered runtime pod's API endpoint.
// Duplicated from controllers to avoid import cycles — the proxy cannot
// import the controllers package.
type RuntimeEndpoint struct {
	PodName  string
	PodIP    string
	Port     int32
	NodeName string
	Ready    bool
}

// URL returns the HTTP URL for the runtime API.
func (e *RuntimeEndpoint) URL() string {
	return fmt.Sprintf("http://%s:%d", e.PodIP, e.Port)
}
