package qdrant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/httpclient"
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

	payload := ChunkToPayload(ch, false, "")
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

func TestGetCollectionVectorSize_DefaultVectors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/collections/test" {
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"size":4,"distance":"Cosine"}}}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(httpclient.NewDefault(), srv.URL, "", "test", "Cosine")
	exists, size, err := c.GetCollectionVectorSize(context.Background())
	if err != nil {
		t.Fatalf("GetCollectionVectorSize err=%v", err)
	}
	if !exists || size != 4 {
		t.Fatalf("exists=%v size=%d want exists=true size=4", exists, size)
	}
}

func TestGetCollectionVectorSize_NamedVectors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/collections/test" {
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"default":{"size":8,"distance":"Cosine"}}}}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(httpclient.NewDefault(), srv.URL, "", "test", "Cosine")
	exists, size, err := c.GetCollectionVectorSize(context.Background())
	if err != nil {
		t.Fatalf("GetCollectionVectorSize err=%v", err)
	}
	if !exists || size != 8 {
		t.Fatalf("exists=%v size=%d want exists=true size=8", exists, size)
	}
}

func TestEnsureCollection_ErrOnVectorSizeMismatch(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/collections/test" {
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"size":4,"distance":"Cosine"}}}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(httpclient.NewDefault(), srv.URL, "", "test", "Cosine")
	if err := c.EnsureCollection(context.Background(), 16); err == nil {
		t.Fatalf("expected error on vector size mismatch")
	}
}

func TestGetFileEmbeddingCache(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/collections/test/points/scroll" {
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"points":[` +
				`{"payload":{"content_hash":"h1"},"vector":[1,2,3]},` +
				`{"payload":{"content_hash":"h2"},"vector":{"default":[4,5,6]}},` +
				`{"payload":{"content_hash":"h3"},"vector":[]}` +
				`],"next_page_offset":null}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(httpclient.NewDefault(), srv.URL, "", "test", "Cosine")
	cache, err := c.GetFileEmbeddingCache(context.Background(), "repo", "file", "model", 10)
	if err != nil {
		t.Fatalf("GetFileEmbeddingCache err=%v", err)
	}
	if len(cache) != 2 {
		t.Fatalf("cache size=%d want 2", len(cache))
	}
	if got := cache["h1"]; len(got) != 3 || got[0] != 1 {
		t.Fatalf("cache[h1]=%v want [1 2 3]", got)
	}
	if got := cache["h2"]; len(got) != 3 || got[0] != 4 {
		t.Fatalf("cache[h2]=%v want [4 5 6]", got)
	}
}
