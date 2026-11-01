package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/flexinfer/flexinfer/backend"
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

type whisperCppToken struct {
	Word        string   `json:"word"`
	Start       *float64 `json:"start"`
	End         *float64 `json:"end"`
	Probability *float64 `json:"probability"`
}

type whisperCppWord struct {
	Word        string  `json:"word"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Probability float64 `json:"probability"`
	probSum     float64
	probN       int
}

// mergeWhisperCppWords joins each segment's tokens into words: a token that
// starts with whitespace, or a segment's first token, opens a word; other
// tokens and punctuation-only tokens extend it. Word probability is the mean
// of its tokens'. It returns false when the body is not a verbose_json
// transcription, already has top-level words, or carries no token timings.
func mergeWhisperCppWords(body []byte) ([]byte, bool) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	if _, ok := doc["words"]; ok {
		return nil, false
	}
	var segments []struct {
		Words []whisperCppToken `json:"words"`
	}
	if raw, ok := doc["segments"]; !ok || json.Unmarshal(raw, &segments) != nil {
		return nil, false
	}
	words := make([]*whisperCppWord, 0, 256)
	for _, seg := range segments {
		var cur *whisperCppWord
		for _, tok := range seg.Words {
			if tok.Start == nil || tok.End == nil || strings.TrimSpace(tok.Word) == "" {
				continue
			}
			start, end := max(*tok.Start, 0), max(*tok.End, 0)
			opens := cur == nil || unicode.IsSpace([]rune(tok.Word)[0])
			if opens && !isPunctuationOnly(tok.Word) {
				cur = &whisperCppWord{Start: start, End: max(end, start)}
				words = append(words, cur)
			}
			if cur == nil {
				continue
			}
			cur.Word += tok.Word
			cur.End = max(cur.End, end)
			if tok.Probability != nil {
				cur.probSum += *tok.Probability
				cur.probN++
			}
		}
	}
	if len(words) == 0 {
		return nil, false
	}
	for _, w := range words {
		w.Word = strings.TrimSpace(w.Word)
		if w.probN > 0 {
			w.Probability = w.probSum / float64(w.probN)
		}
	}
	raw, err := json.Marshal(words)
	if err != nil {
		return nil, false
	}
	doc["words"] = raw
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	return out, true
}

func isPunctuationOnly(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsPunct(r) && !unicode.IsSymbol(r) {
			return false
		}
	}
	return true
}
