package proxy

import (
	"context"
	"log/slog"
	"strconv"
)

// vLLM per-request priority from the QoS class (D3 MR 3).
//
// vLLM 0.32's OpenAI protocol carries an integer `priority` on chat and text
// completion requests (ChatCompletionRequest.priority,
// CompletionRequest.priority): lower values are scheduled first, and any
// value other than 0 is rejected unless the engine runs
// --scheduling-policy priority. The proxy therefore sets the field only on a
// lane with spec.config.schedulingPolicy: priority, and there it always sets
// it, so a caller cannot promote itself past the class it resolved to.

// applyQoSPriority returns body with "priority" set from the request's QoS
// class when the lane runs vLLM priority scheduling; otherwise body unchanged.
func (p *Proxy) applyQoSPriority(ctx context.Context, lane, path string, body []byte) []byte {
	if len(body) == 0 || !isCompletionPath(path) {
		return body
	}
	if p.qosLanePolicy(ctx, lane).mode != qosModePriority {
		return body
	}
	class := qosInteractive
	if d := qosDecisionFrom(ctx); d != nil {
		class = d.class
	}
	// Splice the one field so the rest of the body, including any
	// structured-output schema, is forwarded byte for byte (body_fields.go).
	out, ok := setTopLevelField(body, "priority", []byte(strconv.Itoa(class.vllmPriority())))
	if !ok {
		return body
	}
	slog.Debug("qos: set vLLM priority", "lane", lane, "class", class, "priority", class.vllmPriority())
	return out
}
