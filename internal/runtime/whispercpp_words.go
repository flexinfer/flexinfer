package runtime

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/transcript"
)

// maxWhisperCppResponseBytes bounds the body the ingress buffers to add words.
// A 60-minute meeting's verbose_json is about 3 MB; larger bodies pass through
// unchanged.
const maxWhisperCppResponseBytes = 64 << 20

// ingressRequired reports backends whose API contract is completed by the
// runtime ingress, so they get one even when request drain is off.
func ingressRequired(name string) bool {
	return name == backend.NameWhisperCpp
}

// ingressResponseAdapter returns the ingress response hook a backend's API
// contract needs, or nil.
func ingressResponseAdapter(name string) func(*http.Response) error {
	if name == backend.NameWhisperCpp {
		return addWhisperCppWords
	}
	return nil
}

// addWhisperCppWords gives whisper-server's verbose_json the OpenAI top-level
// "words" list. whisper-server lists sub-word tokens under segments[].words,
// so a client that joins them with spaces (ICC's aligner) splits words apart.
// Any body it cannot parse is forwarded unchanged.
func addWhisperCppWords(resp *http.Response) error {
	req := resp.Request
	if resp.StatusCode != http.StatusOK || req == nil || req.Method != http.MethodPost ||
		req.URL.Path != backend.WhisperCppInferencePath ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWhisperCppResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxWhisperCppResponseBytes {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
		return nil
	}
	_ = resp.Body.Close()
	if merged, ok := mergeWhisperCppWords(body); ok {
		body = merged
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

// mergeWhisperCppWords is transcript.MergeWhisperCppWords. The merge lives in
// pkg/transcript so a diarize job (D5-a) labels the same words with speakers;
// without a speaker the bytes are those this ingress always returned.
func mergeWhisperCppWords(body []byte) ([]byte, bool) {
	return transcript.MergeWhisperCppWords(body)
}
