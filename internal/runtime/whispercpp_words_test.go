package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shape taken from whisper-server v1.9.5 verbose_json: one entry per token
// under each segment, special tokens already dropped.
const whisperCppVerboseJSON = `{"task":"transcribe","language":"english","duration":10.8,
"text":" Welcome to the Radeon VII lane. Thanks, it works.",
"segments":[
 {"id":0,"text":" Welcome to the Radeon VII lane.","start":0.0,"end":5.7,
  "words":[{"word":" Welcome","start":0.0,"end":0.6,"probability":0.9},
           {"word":" to","start":0.6,"end":0.8,"probability":0.8},
           {"word":" the","start":0.8,"end":1.0,"probability":1.0},
           {"word":" Rade","start":1.0,"end":1.4,"probability":0.5},
           {"word":"on","start":1.4,"end":1.7,"probability":0.7},
           {"word":" VII","start":1.7,"end":2.2,"probability":0.6},
           {"word":" lane","start":2.2,"end":2.8,"probability":0.9},
           {"word":".","start":2.8,"end":2.8,"probability":0.95}]},
 {"id":1,"text":"Thanks, it works.","start":5.7,"end":8.0,
  "words":[{"word":"Thanks","start":5.7,"end":6.2,"probability":0.9},
           {"word":",","start":6.2,"end":6.2,"probability":0.9},
           {"word":" it","start":6.3,"end":6.5,"probability":0.9},
           {"word":" works","start":6.5,"end":7.0,"probability":0.9},
           {"word":".","start":7.0,"end":7.1,"probability":0.9}]}]}`

type openAIWords struct {
	Text     string          `json:"text"`
	Segments json.RawMessage `json:"segments"`
	Words    []struct {
		Word  string  `json:"word"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	} `json:"words"`
}

func TestMergeWhisperCppWords(t *testing.T) {
	out, ok := mergeWhisperCppWords([]byte(whisperCppVerboseJSON))
	require.True(t, ok)
	var doc openAIWords
	require.NoError(t, json.Unmarshal(out, &doc))
	got := make([]string, 0, len(doc.Words))
	for _, w := range doc.Words {
		got = append(got, w.Word)
	}
	assert.Equal(t, []string{"Welcome", "to", "the", "Radeon", "VII", "lane.", "Thanks,", "it", "works."}, got)
	assert.Equal(t, 1.0, doc.Words[3].Start)
	assert.Equal(t, 1.7, doc.Words[3].End, "a word spans its first token's start to its last token's end")
	assert.Equal(t, 5.7, doc.Words[6].Start, "a segment's first token opens a word without a leading space")
	assert.Equal(t, " Welcome to the Radeon VII lane. Thanks, it works.", doc.Text, "other fields are preserved")
	assert.NotEmpty(t, doc.Segments)

	var probs struct {
		Words []struct {
			Probability float64 `json:"probability"`
		} `json:"words"`
	}
	require.NoError(t, json.Unmarshal(out, &probs))
	assert.InDelta(t, 0.6, probs.Words[3].Probability, 1e-9, "word probability is the token mean")
}

func TestMergeWhisperCppWordsLeavesOtherBodiesAlone(t *testing.T) {
	for name, body := range map[string]string{
		"not json":         `{"text":`,
		"json format":      `{"text":" hello"}`,
		"has words":        `{"text":"hi","segments":[],"words":[{"word":"hi","start":0,"end":1}]}`,
		"no token timings": `{"text":"hi","segments":[{"words":[{"word":" hi","probability":0.9}]}]}`,
		"only punctuation": `{"text":".","segments":[{"words":[{"word":".","start":0,"end":0}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := mergeWhisperCppWords([]byte(body))
			assert.False(t, ok)
		})
	}
}

func TestWhisperCppIngressAddsWordsOnlyToTranscriptions(t *testing.T) {
	assert.True(t, ingressRequired(backend.NameWhisperCpp))
	assert.False(t, ingressRequired(backend.NameLlamaCpp))
	assert.Nil(t, ingressResponseAdapter(backend.NameLlamaCpp))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/health" {
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		_, _ = io.WriteString(w, whisperCppVerboseJSON)
	}))
	defer upstream.Close()
	ingress, err := newModelIngress(freeIngressTestPort(t), int32(upstream.Listener.Addr().(*net.TCPAddr).Port))
	require.NoError(t, err)
	ingress.proxy.ModifyResponse = ingressResponseAdapter(backend.NameWhisperCpp)
	defer ingress.close()
	ingress.start()
	address := "http://127.0.0.1:" + strconv.Itoa(ingress.listener.Addr().(*net.TCPAddr).Port)
	client := &http.Client{Timeout: 5 * time.Second}

	do := func(method, path string) (http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, address+path, strings.NewReader("multipart body"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.Header, body
	}

	header, body := do(http.MethodPost, backend.WhisperCppInferencePath)
	var doc openAIWords
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Len(t, doc.Words, 9)
	assert.Equal(t, strconv.Itoa(len(body)), header.Get("Content-Length"))

	_, body = do(http.MethodPost, "/inference")
	assert.JSONEq(t, whisperCppVerboseJSON, string(body), "other paths pass through")
	_, body = do(http.MethodGet, "/health")
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

// whispercpp's OpenAI contract depends on the ingress hook, so a runtime
// without request drain still fronts it with an ingress.
func TestWhisperCppGetsIngressWithoutRequestDrain(t *testing.T) {
	m := ingressTestManager(t, false, false)
	binDir := t.TempDir()
	executable := "'" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "'"
	script := "#!/bin/sh\nexec " + executable + " -test.run='^TestRuntimeIngressHelperProcess$' -- \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "whisper-server"), []byte(script), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	port := freeIngressTestPort(t)
	require.NoError(t, m.Load(context.Background(), "asr", LoadRequest{
		Backend: backend.NameWhisperCpp,
		Model:   "/models/ggml.bin",
		Config:  map[string]any{"port": float64(port)},
	}))
	require.Eventually(t, func() bool {
		model, ok := m.Model("asr")
		return ok && model.State == string(ModelStateReady)
	}, 5*time.Second, 5*time.Millisecond)
	m.mu.RLock()
	model := m.models["asr"]
	m.mu.RUnlock()
	require.NotNil(t, model.ingress, "whispercpp must get an ingress without request drain")
	assert.NotNil(t, model.ingress.proxy.ModifyResponse)
	assert.Equal(t, port, model.Port)
	assert.NotEqual(t, model.Port, modelBackendPort(model), "the child binds a private port")
	require.NoError(t, m.Unload(context.Background(), "asr"))
}
