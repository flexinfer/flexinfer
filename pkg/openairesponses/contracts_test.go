package openairesponses

import "testing"

func TestParseContextMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ContextMode
		wantErr bool
	}{
		{name: "default empty", input: "", want: ContextModeChain},
		{name: "chain", input: "chain", want: ContextModeChain},
		{name: "conversation uppercase", input: "CONVERSATION", want: ContextModeConversation},
		{name: "stateless", input: "stateless", want: ContextModeStateless},
		{name: "invalid", input: "unknown", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseContextMode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected parse error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("mode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContextStrategyValidate(t *testing.T) {
	tests := []struct {
		name    string
		strat   ContextStrategy
		wantErr bool
	}{
		{
			name: "chain with previous id",
			strat: ContextStrategy{
				Mode:               ContextModeChain,
				PreviousResponseID: "resp_123",
			},
		},
		{
			name: "conversation requires id",
			strat: ContextStrategy{
				Mode: ContextModeConversation,
			},
			wantErr: true,
		},
		{
			name: "conversation with id",
			strat: ContextStrategy{
				Mode:           ContextModeConversation,
				ConversationID: "conv_123",
			},
		},
		{
			name: "stateless rejects prior ids",
			strat: ContextStrategy{
				Mode:               ContextModeStateless,
				PreviousResponseID: "resp_123",
			},
			wantErr: true,
		},
		{
			name: "mutually exclusive ids",
			strat: ContextStrategy{
				Mode:               ContextModeChain,
				PreviousResponseID: "resp_123",
				ConversationID:     "conv_123",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.strat.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestTurnRequestValidate(t *testing.T) {
	valid := TurnRequest{
		Model: "gpt-5",
		Context: ContextStrategy{
			Mode: ContextModeChain,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	invalid := valid
	invalid.Model = ""
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected missing model error")
	}
}
