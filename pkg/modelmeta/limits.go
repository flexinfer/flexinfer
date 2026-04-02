package modelmeta

import (
	"strconv"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const (
	AnnotationLiteLLMContextWindow   = "litellm.flexinfer.ai/context-window"
	AnnotationLiteLLMMaxInputTokens  = "litellm.flexinfer.ai/max-input-tokens"
	AnnotationLiteLLMMaxOutputTokens = "litellm.flexinfer.ai/max-output-tokens"
)

type TokenLimits struct {
	ContextWindow   int
	MaxInputTokens  int
	MaxOutputTokens int
}

func ResolveTokenLimits(spec *aiv1alpha2.ModelSpec) TokenLimits {
	if spec == nil {
		return TokenLimits{}
	}

	contextWindow := firstPositive(
		spec.ConfigInt("maxModelLen", 0),
		spec.ConfigInt("maxTotalSeqLength", 0),
		spec.ConfigInt("contextWindowSize", 0),
		spec.ConfigInt("contextSize", 0),
	)
	maxInput := spec.ConfigInt("maxInputTokens", 0)
	maxOutput := spec.ConfigInt("maxOutputTokens", 0)

	if maxInput <= 0 {
		maxInput = contextWindow
	}
	if maxOutput <= 0 {
		maxOutput = contextWindow
	}

	if contextWindow > 0 {
		if maxInput <= 0 || maxInput > contextWindow {
			maxInput = contextWindow
		}
		if maxOutput <= 0 || maxOutput > contextWindow {
			maxOutput = contextWindow
		}
	}

	return TokenLimits{
		ContextWindow:   contextWindow,
		MaxInputTokens:  maxInput,
		MaxOutputTokens: maxOutput,
	}
}

func ApplyTokenLimitAnnotations(annotations map[string]string, limits TokenLimits) {
	if annotations == nil {
		return
	}
	if limits.ContextWindow > 0 {
		annotations[AnnotationLiteLLMContextWindow] = strconv.Itoa(limits.ContextWindow)
	}
	if limits.MaxInputTokens > 0 {
		annotations[AnnotationLiteLLMMaxInputTokens] = strconv.Itoa(limits.MaxInputTokens)
	}
	if limits.MaxOutputTokens > 0 {
		annotations[AnnotationLiteLLMMaxOutputTokens] = strconv.Itoa(limits.MaxOutputTokens)
	}
}

func ApplyTokenLimitMetadata(metadata map[string]any, limits TokenLimits) {
	if metadata == nil {
		return
	}
	if limits.ContextWindow > 0 {
		metadata["context_window"] = limits.ContextWindow
	}
	if limits.MaxInputTokens > 0 {
		metadata["max_input_tokens"] = limits.MaxInputTokens
	}
	if limits.MaxOutputTokens > 0 {
		metadata["max_output_tokens"] = limits.MaxOutputTokens
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
