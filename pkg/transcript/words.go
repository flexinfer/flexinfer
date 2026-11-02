// Package transcript holds the OpenAI verbose_json word list the ASR lanes
// produce and the speaker attribution D5-a's diarize jobs add to it
// (.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.5).
package transcript

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Word is one entry of an OpenAI verbose_json top-level "words" list. Speaker
// is set only on a diarized transcript, so a plain transcription serializes
// exactly as it did before speakers existed.
type Word struct {
	Word        string   `json:"word"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	Probability *float64 `json:"probability,omitempty"`
	Speaker     string   `json:"speaker,omitempty"`
}

type whisperCppToken struct {
	Word        string   `json:"word"`
	Start       *float64 `json:"start"`
	End         *float64 `json:"end"`
	Probability *float64 `json:"probability"`
}

// MergeWhisperCppWords gives whisper-server's verbose_json the OpenAI
// top-level "words" list: it joins each segment's sub-word tokens into words.
// A token that starts with whitespace, or a segment's first token, opens a
// word; other tokens and punctuation-only tokens extend it. Word probability
// is the mean of its tokens' (0 when none carry one). It returns false when
// the body is not a verbose_json transcription, already has top-level words,
// or carries no token timings.
func MergeWhisperCppWords(body []byte) ([]byte, bool) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	if _, ok := doc["words"]; ok {
		return nil, false
	}
	words, ok := whisperCppWords(doc["segments"])
	if !ok {
		return nil, false
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

// whisperCppWords merges the tokens under segments[].words into words.
func whisperCppWords(rawSegments json.RawMessage) ([]Word, bool) {
	var segments []struct {
		Words []whisperCppToken `json:"words"`
	}
	if len(rawSegments) == 0 || json.Unmarshal(rawSegments, &segments) != nil {
		return nil, false
	}
	type acc struct {
		word    Word
		probSum float64
		probN   int
	}
	merged := make([]*acc, 0, 256)
	for _, seg := range segments {
		var cur *acc
		for _, tok := range seg.Words {
			if tok.Start == nil || tok.End == nil || strings.TrimSpace(tok.Word) == "" {
				continue
			}
			start, end := max(*tok.Start, 0), max(*tok.End, 0)
			opens := cur == nil || unicode.IsSpace([]rune(tok.Word)[0])
			if opens && !isPunctuationOnly(tok.Word) {
				cur = &acc{word: Word{Start: start, End: max(end, start)}}
				merged = append(merged, cur)
			}
			if cur == nil {
				continue
			}
			cur.word.Word += tok.Word
			cur.word.End = max(cur.word.End, end)
			if tok.Probability != nil {
				cur.probSum += *tok.Probability
				cur.probN++
			}
		}
	}
	if len(merged) == 0 {
		return nil, false
	}
	words := make([]Word, len(merged))
	for i, a := range merged {
		w := a.word
		w.Word = strings.TrimSpace(w.Word)
		p := 0.0
		if a.probN > 0 {
			p = a.probSum / float64(a.probN)
		}
		w.Probability = &p
		words[i] = w
	}
	return words, true
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
