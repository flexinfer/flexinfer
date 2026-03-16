package runtime

// RuntimeAPIPort is the well-known port where the flexinfer-runtime
// sidecar listens for model load/unload/health commands. Shared between
// the controller (runtime_controller.go) and the proxy (runtime_cache.go).
const RuntimeAPIPort int32 = 8080
