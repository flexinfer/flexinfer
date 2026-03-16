package registry

import "time"

const (
	// DefaultHTTPTimeout is the default timeout for registry HTTP clients.
	DefaultHTTPTimeout = 30 * time.Second

	// DefaultHuggingFaceBaseURL is the default HuggingFace API base URL.
	DefaultHuggingFaceBaseURL = "https://huggingface.co"

	// DefaultOllamaBaseURL is the default Ollama registry base URL.
	DefaultOllamaBaseURL = "https://ollama.com"

	// DefaultListLimit is the default page size for model list queries.
	DefaultListLimit = 20
)
