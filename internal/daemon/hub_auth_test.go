package daemon

import (
	"errors"
	"testing"
)

func TestIsHubAuthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "html", err: errors.New("hub returned HTML instead of JSON"), want: true},
		{name: "invalid_char", err: errors.New("invalid character '<' looking for beginning of value"), want: true},
		{name: "401", err: errors.New("fetch hub hosts failed (401)"), want: true},
		{name: "403", err: errors.New("fetch hub hosts failed (403)"), want: true},
		{name: "auth_required", err: errors.New("auth required"), want: true},
		{name: "other", err: errors.New("connection refused"), want: false},
	}

	for _, tt := range tests {
		if got := isHubAuthError(tt.err); got != tt.want {
			t.Fatalf("%s: got %v want %v", tt.name, got, tt.want)
		}
	}
}
