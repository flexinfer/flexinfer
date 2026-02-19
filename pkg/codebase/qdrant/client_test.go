package qdrant

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
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
	err := c.EnsureCollection(context.Background(), 16)
	if err == nil {
		t.Fatalf("expected error on vector size mismatch")
	}
	if !IsVectorSizeMismatch(err) {
		t.Fatalf("expected vector size mismatch error, got: %v", err)
	}
	var mismatchErr *VectorSizeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("expected VectorSizeMismatchError, got: %T", err)
	}
	if mismatchErr.Existing != 4 || mismatchErr.Expected != 16 {
		t.Fatalf("mismatch values: existing=%d expected=%d", mismatchErr.Existing, mismatchErr.Expected)
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

func TestToPointID_IsDeterministicUUID(t *testing.T) {
	t.Parallel()

	id1 := toPointID("repo:file:1")
	id2 := toPointID("repo:file:1")
	id3 := toPointID("repo:file:2")

	if id1 != id2 {
		t.Fatalf("toPointID must be deterministic: %q != %q", id1, id2)
	}
	if id1 == id3 {
		t.Fatalf("different inputs should not produce the same point id: %q", id1)
	}

	uuidRE := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRE.MatchString(id1) {
		t.Fatalf("toPointID(%q) did not return UUIDv5 format: %q", "repo:file:1", id1)
	}
}

func TestPointsToJSON_UsesConvertedPointID(t *testing.T) {
	t.Parallel()

	points := []Point{
		{
			ID:     "chunk-raw-id",
			Vector: []float64{1, 2, 3},
			Payload: map[string]any{
				"id": "chunk-raw-id",
			},
		},
	}

	raw := pointsToJSON(points)
	gotID, _ := raw[0]["id"].(string)
	if gotID != toPointID("chunk-raw-id") {
		t.Fatalf("pointsToJSON id=%q want %q", gotID, toPointID("chunk-raw-id"))
	}
	payloadID, _ := raw[0]["payload"].(map[string]any)["id"].(string)
	if payloadID != "chunk-raw-id" {
		t.Fatalf("payload id mutated: got %q", payloadID)
	}
}

func TestDoJSON_404WithoutCollectionMessageIsNotCollectionNotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/collections/test/points/some-point" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":{"error":"point not found"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(httpclient.NewDefault(), srv.URL, "", "test", "Cosine")
	err := c.doJSON(context.Background(), http.MethodGet, "/collections/test/points/some-point", nil, nil)
	if err == nil {
		t.Fatalf("expected 404 error")
	}
	if errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("expected non-collection error, got ErrCollectionNotFound")
	}
	if !strings.Contains(err.Error(), "qdrant HTTP 404") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecreateCollection_DeletesThenCreates(t *testing.T) {
	t.Parallel()

	var (
		seenDelete bool
		seenGet    bool
		seenPut    bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/collections/test":
			seenDelete = true
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/collections/test":
			seenGet = true
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":{"error":"collection not found"}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/test":
			seenPut = true
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(httpclient.NewDefault(), srv.URL, "", "test", "Cosine")
	if err := c.RecreateCollection(context.Background(), 8); err != nil {
		t.Fatalf("RecreateCollection err=%v", err)
	}
	if !seenDelete || !seenGet || !seenPut {
		t.Fatalf("expected delete/get/put sequence; seen delete=%v get=%v put=%v", seenDelete, seenGet, seenPut)
	}
}
