package backend

const (
	// DefaultGPULayersAll is the number of GPU layers to offload when using all
	// available GPU memory. The sentinel value 999 tells llama.cpp to offload
	// every layer to the GPU.
	DefaultGPULayersAll = 999

	// DefaultMLCGPUBytesMaxwell is the default GPU memory budget (bytes) for
	// Maxwell-architecture NVIDIA GPUs (6 GB cards, ~5 GB usable).
	DefaultMLCGPUBytesMaxwell int64 = 5_000_000_000

	// DefaultMLCGPUBytesLarge is the default GPU memory budget (bytes) for
	// large GPUs like the RX 7900 XTX (~23 GB usable).
	DefaultMLCGPUBytesLarge int64 = 24_696_061_952

	// Backend listening port constants. Each backend binds to a well-known
	// port inside the model container. These are referenced by Port() methods
	// and by probe constructors.
	PortVLLM      int32 = 8000
	PortMLCLLM    int32 = 8000
	PortDiffusers int32 = 8000
	PortLlamaCpp  int32 = 8080
	PortComfyUI   int32 = 8188
	PortOllama    int32 = 11434
	PortSteam     int32 = 27036
	// PortSunshine is Sunshine's Moonlight HTTP control port (base 47989). It is
	// the canonical "is the gaming host up" TCP target; the HTTPS Moonlight port
	// (47984), HTTPS web UI (47990), RTSP (48010/tcp) and the UDP media ports
	// (47998-48010) are exposed at the DaemonSet/networking layer, not here.
	PortSunshine int32 = 47989
)
