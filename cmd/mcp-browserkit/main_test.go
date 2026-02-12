package main

import "testing"

func TestNormalizeImageDataURL_RawBase64(t *testing.T) {
	got, err := normalizeImageDataURL("image/png", "AQID")
	if err != nil {
		t.Fatalf("normalizeImageDataURL returned error: %v", err)
	}
	want := "data:image/png;base64,AQID"
	if got != want {
		t.Fatalf("unexpected data URL\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalizeImageDataURL_ExistingDataURL(t *testing.T) {
	got, err := normalizeImageDataURL("image/png", "data:image/jpeg;base64,AQID")
	if err != nil {
		t.Fatalf("normalizeImageDataURL returned error: %v", err)
	}
	want := "data:image/jpeg;base64,AQID"
	if got != want {
		t.Fatalf("unexpected data URL\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalizeImageDataURL_InvalidData(t *testing.T) {
	if _, err := normalizeImageDataURL("image/png", "not-base64@@@"); err == nil {
		t.Fatalf("expected error for invalid base64 payload")
	}
}

func TestNormalizeImageDataURL_InvalidDataURL(t *testing.T) {
	if _, err := normalizeImageDataURL("image/png", "data:image/png,abc"); err == nil {
		t.Fatalf("expected error for non-base64 data URL payload")
	}
}
