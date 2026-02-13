package proxy

import (
	"encoding/json"
	"testing"
)

func TestSpliceModelField(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		replacement string
		wantModel   string
		wantNil     bool
	}{
		{
			name:        "simple replacement",
			body:        `{"model":"my-alias","temperature":0.7}`,
			replacement: "Qwen/Qwen2.5-7B-Instruct",
			wantModel:   "Qwen/Qwen2.5-7B-Instruct",
		},
		{
			name:        "model with spaces around colon",
			body:        `{"model" : "my-alias", "messages": []}`,
			replacement: "meta-llama/Llama-3-8B",
			wantModel:   "meta-llama/Llama-3-8B",
		},
		{
			name:        "model not first field",
			body:        `{"temperature":0.7,"model":"old","stream":true}`,
			replacement: "new-model",
			wantModel:   "new-model",
		},
		{
			name:        "model with escaped quotes in value",
			body:        `{"model":"has\"quote","ok":true}`,
			replacement: "clean-model",
			wantModel:   "clean-model",
		},
		{
			name:        "replacement with special chars",
			body:        `{"model":"old"}`,
			replacement: `org/model-v2.1`,
			wantModel:   `org/model-v2.1`,
		},
		{
			name:        "replacement with newline is escaped",
			body:        `{"model":"old"}`,
			replacement: "line1\nline2",
			wantModel:   "line1\nline2",
		},
		{
			name:    "no model field",
			body:    `{"temperature":0.7}`,
			wantNil: true,
		},
		{
			name:    "model value is number",
			body:    `{"model":42}`,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := spliceModelField([]byte(tt.body), tt.replacement)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %q", string(result))
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}

			// Verify result is valid JSON with correct model
			var parsed map[string]interface{}
			if err := json.Unmarshal(result, &parsed); err != nil {
				t.Fatalf("result is not valid JSON: %v\nresult: %q", err, string(result))
			}
			model, ok := parsed["model"].(string)
			if !ok {
				t.Fatalf("model field missing or not string in result: %v", parsed)
			}
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
		})
	}
}

func TestRewriteModelInBody(t *testing.T) {
	p := &Proxy{}

	body := []byte(`{"model":"user-alias","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	result := p.rewriteModelInBody(body, "Qwen/Qwen2.5-7B-Instruct")

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if parsed["model"] != "Qwen/Qwen2.5-7B-Instruct" {
		t.Errorf("model = %v, want Qwen/Qwen2.5-7B-Instruct", parsed["model"])
	}
	// Verify other fields preserved
	if parsed["stream"] != true {
		t.Error("stream field was lost")
	}
}

func TestJsonEscapeString(t *testing.T) {
	t.Skip("jsonEscapeString was removed; spliceModelField uses strconv.Quote for escaping")
}

func BenchmarkRewriteModelInBody_Surgical(b *testing.B) {
	body := []byte(`{"model":"my-model","messages":[{"role":"user","content":"hello world"}],"temperature":0.7,"stream":true}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spliceModelField(body, "Qwen/Qwen2.5-7B-Instruct")
	}
}

func BenchmarkRewriteModelInBody_FullParse(b *testing.B) {
	body := []byte(`{"model":"my-model","messages":[{"role":"user","content":"hello world"}],"temperature":0.7,"stream":true}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			b.Fatal(err)
		}
		data["model"] = "Qwen/Qwen2.5-7B-Instruct"
		if _, err := json.Marshal(data); err != nil {
			b.Fatal(err)
		}
	}
}
