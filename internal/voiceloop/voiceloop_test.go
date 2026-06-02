package voiceloop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStack mocks the three proxy voice routes for the loop test.
func fakeStack(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	asrCount := 0
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		assert.Equal(t, "whisper-large-v3-turbo", r.FormValue("model"))
		_, _, err := r.FormFile("file")
		require.NoError(t, err)
		asrCount++
		calls = append(calls, "asr")
		text := "what time is the meeting" // first call: transcribe input audio
		if asrCount > 1 {
			text = "the meeting is at noon" // verify call: transcribe TTS output
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), "what time is the meeting")
		assert.Contains(t, string(body), "gemma4-26b-a4b-gptq")
		calls = append(calls, "chat")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"The meeting is at noon."}}]}`))
	})

	mux.HandleFunc("/v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "The meeting is at noon.", req["input"])
		assert.Equal(t, "kokoro", req["model"])
		assert.Equal(t, "af_heart", req["voice"])
		calls = append(calls, "tts")
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFFsynthetic-wav-bytes"))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestRun_FullLoopWithVerify(t *testing.T) {
	srv, calls := fakeStack(t)
	c := NewClient(srv.URL, srv.Client())

	cfg := Config{
		ChatModel: "gemma4-26b-a4b-gptq",
		ASRModel:  "whisper-large-v3-turbo",
		TTSModel:  "kokoro",
		Voice:     "af_heart",
		Format:    "wav",
		MaxTokens: 128,
		Verify:    true,
	}
	res, err := Run(context.Background(), c, cfg, []byte("fake-input-audio"), "input.wav", "")
	require.NoError(t, err)

	assert.Equal(t, "what time is the meeting", res.Transcript)
	assert.Equal(t, "what time is the meeting", res.Prompt)
	assert.Equal(t, "The meeting is at noon.", res.Reply)
	assert.Contains(t, string(res.Audio), "synthetic-wav-bytes")
	assert.Equal(t, "audio/wav", res.ContentType)
	assert.Equal(t, "the meeting is at noon", res.Verification)
	// ASR runs twice (input + verify), with chat+tts between.
	assert.Equal(t, []string{"asr", "chat", "tts", "asr"}, *calls)
}

func TestRun_TextInputNoVerify(t *testing.T) {
	srv, calls := fakeStack(t)
	c := NewClient(srv.URL, srv.Client())

	cfg := Config{
		ChatModel: "gemma4-26b-a4b-gptq",
		ASRModel:  "whisper-large-v3-turbo",
		TTSModel:  "kokoro",
		Voice:     "af_heart",
		Format:    "wav",
		MaxTokens: 128,
		Verify:    false,
	}
	res, err := Run(context.Background(), c, cfg, nil, "", "what time is the meeting")
	require.NoError(t, err)

	assert.Empty(t, res.Transcript) // no audio input → no ASR
	assert.Equal(t, "what time is the meeting", res.Prompt)
	assert.Equal(t, "The meeting is at noon.", res.Reply)
	assert.Empty(t, res.Verification)
	assert.Equal(t, []string{"chat", "tts"}, *calls)
}

func TestRun_NoInput(t *testing.T) {
	srv, _ := fakeStack(t)
	c := NewClient(srv.URL, srv.Client())
	_, err := Run(context.Background(), c, Config{}, nil, "", "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no input")
}
