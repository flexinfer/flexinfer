/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package commands

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunImagePin(t *testing.T) {
	const digest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.Path == "/v2/flexinfer/runtime/manifests/master" {
			w.Header().Set("Docker-Content-Digest", digest)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	ref := host + "/flexinfer/runtime:master"

	// runImagePin reads package-global flag vars; restore them after the test.
	t.Cleanup(func() {
		imagePinQuiet = false
		imagePinInsecure = false
		imagePinUsername = ""
		imagePinPassword = ""
	})

	newCmd := func(out *bytes.Buffer) *cobra.Command {
		c := &cobra.Command{}
		c.SetOut(out)
		c.SetErr(io.Discard)
		c.SetContext(context.Background())
		return c
	}

	t.Run("quiet prints digest only", func(t *testing.T) {
		imagePinInsecure = true
		imagePinQuiet = true
		buf := &bytes.Buffer{}
		if err := runImagePin(newCmd(buf), []string{ref}); err != nil {
			t.Fatalf("runImagePin: %v", err)
		}
		if got := strings.TrimSpace(buf.String()); got != digest {
			t.Errorf("stdout = %q, want %q", got, digest)
		}
	})

	t.Run("default prints pinned reference", func(t *testing.T) {
		imagePinInsecure = true
		imagePinQuiet = false
		buf := &bytes.Buffer{}
		if err := runImagePin(newCmd(buf), []string{ref}); err != nil {
			t.Fatalf("runImagePin: %v", err)
		}
		want := host + "/flexinfer/runtime@" + digest
		if got := strings.TrimSpace(buf.String()); got != want {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("resolve error surfaces", func(t *testing.T) {
		imagePinInsecure = true
		imagePinQuiet = true
		buf := &bytes.Buffer{}
		if err := runImagePin(newCmd(buf), []string{host + "/missing/repo:tag"}); err == nil {
			t.Fatal("expected error for missing repo, got nil")
		}
	})
}
