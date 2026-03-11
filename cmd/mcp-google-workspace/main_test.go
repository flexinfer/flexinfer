package main

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	docsapi "google.golang.org/api/docs/v1"
)

func TestBuildRFC822Message(t *testing.T) {
	raw, err := buildRFC822Message([]string{"to@example.com"}, []string{"cc@example.com"}, nil, "Subject", "hello world", "", "reply@example.com")
	if err != nil {
		t.Fatalf("buildRFC822Message returned error: %v", err)
	}
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(raw)
	if err != nil {
		t.Fatalf("decode returned error: %v", err)
	}
	msg := string(decoded)
	if !strings.Contains(msg, "To: to@example.com") {
		t.Fatalf("expected To header in %q", msg)
	}
	if !strings.Contains(msg, "Subject: Subject") {
		t.Fatalf("expected Subject header in %q", msg)
	}
	if !strings.Contains(msg, "hello world") {
		t.Fatalf("expected body in %q", msg)
	}
}

func TestDocumentEndIndex(t *testing.T) {
	doc := &docsapi.Document{
		Body: &docsapi.Body{
			Content: []*docsapi.StructuralElement{
				{EndIndex: 1},
				{EndIndex: 12},
			},
		},
	}
	if got := documentEndIndex(doc); got != 11 {
		t.Fatalf("expected end index 11, got %d", got)
	}
}

func TestOptionalStringArg(t *testing.T) {
	if got, ok := optionalStringArg(map[string]any{"summary": "  hello  "}, "summary"); !ok || got != "hello" {
		t.Fatalf("optionalStringArg() = (%q,%v), want (%q,true)", got, ok, "hello")
	}
	if got, ok := optionalStringArg(map[string]any{}, "summary"); ok || got != "" {
		t.Fatalf("optionalStringArg() missing = (%q,%v), want (\"\",false)", got, ok)
	}
}

func TestOptionalStringSliceArg(t *testing.T) {
	got, ok := optionalStringSliceArg(map[string]any{
		"attendees": []any{" a@example.com ", "b@example.com"},
	}, "attendees")
	if !ok {
		t.Fatal("optionalStringSliceArg() should mark attendees as present")
	}
	want := []string{"a@example.com", "b@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("optionalStringSliceArg() = %#v, want %#v", got, want)
	}

	got, ok = optionalStringSliceArg(map[string]any{}, "attendees")
	if ok || got != nil {
		t.Fatalf("optionalStringSliceArg() missing = (%#v,%v), want (nil,false)", got, ok)
	}
}
