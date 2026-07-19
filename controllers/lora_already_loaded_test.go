package controllers

import (
	"net/http"
	"testing"
)

func TestIsLoRAAdapterAlreadyLoaded(t *testing.T) {
	// Captured live from vLLM 2026-07-19: the message nests under "error".
	nested := []byte(`{"error":{"message":"The lora adapter 'nsfw-rp' has already been loaded. If you want to load the adapter in place, set 'load_inplace' to True.","type":"InvalidUserInput","param":null,"code":400}}`)
	flat := []byte(`{"message":"The lora adapter 'nsfw-rp' has already been loaded."}`)
	other := []byte(`{"error":{"message":"some other 400"}}`)

	cases := []struct {
		name       string
		statusCode int
		body       []byte
		adapter    string
		want       bool
	}{
		{"nested vLLM error shape", http.StatusBadRequest, nested, "nsfw-rp", true},
		{"flat message shape", http.StatusBadRequest, flat, "nsfw-rp", true},
		{"different adapter name", http.StatusBadRequest, nested, "other-adapter", false},
		{"unrelated 400", http.StatusBadRequest, other, "nsfw-rp", false},
		{"non-400 status", http.StatusInternalServerError, nested, "nsfw-rp", false},
		{"invalid json", http.StatusBadRequest, []byte("not json"), "nsfw-rp", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLoRAAdapterAlreadyLoaded(tc.statusCode, tc.body, tc.adapter); got != tc.want {
				t.Fatalf("isLoRAAdapterAlreadyLoaded(%d, %s, %q) = %v, want %v",
					tc.statusCode, tc.body, tc.adapter, got, tc.want)
			}
		})
	}
}
