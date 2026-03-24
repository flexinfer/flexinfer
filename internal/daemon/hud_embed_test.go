package daemon

import "testing"

func TestFirstNonEmpty_ReturnsFirst(t *testing.T) {
	got := firstNonEmpty("hello", "world")
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestFirstNonEmpty_SkipsEmpty(t *testing.T) {
	got := firstNonEmpty("", "second")
	if got != "second" {
		t.Errorf("got %q, want %q", got, "second")
	}
}

func TestFirstNonEmpty_SkipsWhitespace(t *testing.T) {
	got := firstNonEmpty("  ", "\t", "real")
	if got != "real" {
		t.Errorf("got %q, want %q", got, "real")
	}
}

func TestFirstNonEmpty_AllEmpty(t *testing.T) {
	got := firstNonEmpty("", "  ", "\t")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFirstNonEmpty_NoArgs(t *testing.T) {
	got := firstNonEmpty()
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFirstNonEmpty_SingleValue(t *testing.T) {
	got := firstNonEmpty("only")
	if got != "only" {
		t.Errorf("got %q, want %q", got, "only")
	}
}
