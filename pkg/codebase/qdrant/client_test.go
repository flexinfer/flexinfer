package qdrant

import (
	"reflect"
	"testing"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

func TestNormalizeCallToken(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"foo":          "foo",
		"obj.foo":      "foo",
		"bar::baz":     "baz",
		"foo::<T>":     "foo",
		"pkg.Foo<T>":   "Foo",
		" call() ":     "call",
		"weird-name()": "weirdname",
	}

	for in, want := range cases {
		if got := normalizeCallToken(in); got != want {
			t.Fatalf("normalizeCallToken(%q)=%q want %q", in, got, want)
		}
	}
}

func TestChunkToPayload_IncludesCallNames(t *testing.T) {
	t.Parallel()

	ch := schema.Chunk{
		ID:        "id",
		RepoID:    "repo",
		FilePath:  "file",
		Calls:     []string{"obj.foo", "bar::baz", "foo"},
		SchemaVer: schema.Version,
	}

	payload := ChunkToPayload(ch, false)
	raw, ok := payload["call_names"]
	if !ok {
		t.Fatalf("expected call_names in payload")
	}
	got, ok := raw.([]string)
	if !ok {
		t.Fatalf("expected call_names to be []string, got %T", raw)
	}

	want := []string{"baz", "foo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call_names=%v want %v", got, want)
	}
}
