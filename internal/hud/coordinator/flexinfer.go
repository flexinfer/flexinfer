package coordinator

// This file re-exports types from pkg/flexinfer so the coordinator subsystem
// files (summarizer, compressor, etc.) continue to work without import changes.

import (
	"github.com/crb2nu/loom/pkg/flexinfer"
)

// Type aliases — coordinator code can still write FlexInferClient, ChatMessage, etc.
type (
	FlexInferClient        = flexinfer.Client
	ChatMessage            = flexinfer.ChatMessage
	ChatCompletionRequest  = flexinfer.ChatCompletionRequest
	ChatCompletionResponse = flexinfer.ChatCompletionResponse
	ChatCompletionChoice   = flexinfer.ChatCompletionChoice
	ChatCompletionUsage    = flexinfer.ChatCompletionUsage
	ModelInfo              = flexinfer.ModelInfo
)

// modelsResponse alias for test compatibility.
type modelsResponse = flexinfer.ModelsResponse

// NewFlexInferClient delegates to flexinfer.NewClient.
var NewFlexInferClient = flexinfer.NewClient
