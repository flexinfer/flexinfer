// Package weaver implements the weaver domain -- FlexInfer-based
// multi-domain query weavertion endpoints for the HUD dashboard.
package weaver

import (
	"encoding/json"
	"net/http"
)

// BridgeCaller calls the daemon IPC for weaver data.
type BridgeCaller interface {
	Call(method string, params any) (json.RawMessage, error)
}

// Deps defines the dependencies the weaver domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	WeaverBridge() BridgeCaller
}
