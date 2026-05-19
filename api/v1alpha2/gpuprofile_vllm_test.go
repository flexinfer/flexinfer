/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha2

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestVLLMCapabilities_JSONRoundTrip verifies the Wave 1 schema-only VLLM
// capability matrix fields survive a JSON marshal/unmarshal round-trip.
// Reference: .loom/21-product-spec-vllm-feature-parity-2026-05-15.md (Slice 1).
func TestVLLMCapabilities_JSONRoundTrip(t *testing.T) {
	enforceEager := true
	original := BackendProfile{
		Support: "full",
		Image:   "registry.example/vllm@sha256:abc",
		VLLM: &VLLMCapabilities{
			V1Engine:           "supported",
			PiecewiseGraphs:    "experimental",
			FlashAttention:     "ck",
			FusedMoETriton:     "experimental",
			FP8KVEmulation:     "experimental",
			MarlinINT4:         "unsupported",
			AudioTranscription: "experimental",
			Defaults: &VLLMDefaults{
				CudagraphMode: "NONE",
				EnforceEager:  &enforceEager,
				KVCacheDtype:  "auto",
			},
		},
	}

	data, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var roundTrip BackendProfile
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(original, roundTrip) {
		t.Errorf("round-trip mismatch:\noriginal:  %+v\nroundTrip: %+v", original, roundTrip)
	}
}

// TestVLLMCapabilities_OmitemptyWhenUnset confirms the new fields are absent
// from JSON output when VLLM is nil, preserving backwards compatibility with
// existing GPUProfile manifests that do not carry the capability matrix.
func TestVLLMCapabilities_OmitemptyWhenUnset(t *testing.T) {
	bp := BackendProfile{Support: "full"}
	data, err := json.Marshal(&bp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	want := `{"support":"full"}`
	if got != want {
		t.Errorf("BackendProfile without VLLM: got %s, want %s", got, want)
	}
}

// TestVLLMCapabilities_DeepCopy exercises the generated deepcopy on the new
// types and confirms pointer fields are not shared with the source.
func TestVLLMCapabilities_DeepCopy(t *testing.T) {
	enforceEager := true
	source := &VLLMCapabilities{
		V1Engine: "supported",
		Defaults: &VLLMDefaults{
			CudagraphMode: "PIECEWISE",
			EnforceEager:  &enforceEager,
		},
	}

	clone := source.DeepCopy()
	if clone == source {
		t.Fatalf("DeepCopy returned same pointer")
	}
	if clone.Defaults == source.Defaults {
		t.Errorf("Defaults pointer shared after DeepCopy")
	}
	if clone.Defaults.EnforceEager == source.Defaults.EnforceEager {
		t.Errorf("EnforceEager pointer shared after DeepCopy")
	}

	*clone.Defaults.EnforceEager = false
	if *source.Defaults.EnforceEager != true {
		t.Errorf("mutation through clone leaked to source")
	}
}

// TestVLLMCapabilities_AudioTranscription verifies the new Whisper-support
// capability field round-trips and honors omitempty when unset.
// Reference: .loom/asr-diarization-7900xtx-plan-2026-05-18.md (Slice 2).
func TestVLLMCapabilities_AudioTranscription(t *testing.T) {
	t.Run("omitempty when unset", func(t *testing.T) {
		caps := &VLLMCapabilities{V1Engine: "supported"}
		data, err := json.Marshal(caps)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		got := string(data)
		want := `{"v1Engine":"supported"}`
		if got != want {
			t.Errorf("AudioTranscription unset: got %s, want %s", got, want)
		}
	})

	for _, value := range []string{"supported", "experimental", "unsupported"} {
		t.Run(value, func(t *testing.T) {
			original := &VLLMCapabilities{AudioTranscription: value}
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var roundTrip VLLMCapabilities
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if roundTrip.AudioTranscription != value {
				t.Errorf("round-trip: got %q, want %q", roundTrip.AudioTranscription, value)
			}
		})
	}
}
