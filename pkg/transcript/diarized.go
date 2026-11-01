package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
)

// DiarizationBlock is the "diarization" member of a diarized transcript: the
// diarization lane that ran and the turns it returned, unchanged, so a client
// can run its own alignment or score the turns against a reference RTTM.
type DiarizationBlock struct {
	// Model is the diarization lane (a pyannote Model).
	Model string `json:"model"`
	// Pipeline is the lane's pipeline id (pyannote/speaker-diarization-3.1).
	Pipeline    string `json:"pipeline,omitempty"`
	NumSpeakers int    `json:"num_speakers"`
	Segments    []Turn `json:"segments"`
}

// Diarized is the part of a diarized transcript a reader needs. The document
// also keeps every field of the ASR lane's verbose_json (task, language,
// segments, ...).
type Diarized struct {
	Text        string           `json:"text"`
	Duration    float64          `json:"duration"`
	Words       []Word           `json:"words"`
	Speakers    []string         `json:"speakers"`
	Utterances  []Utterance      `json:"utterances"`
	Diarization DiarizationBlock `json:"diarization"`
}

// Attribution counts the words of a diarized transcript.
type Attribution struct {
	Words    int
	Labelled int
}

// ErrNoWords means the ASR lane's body has no word timings to attribute.
var ErrNoWords = errors.New("the transcription has no word timings: the ASR lane must answer verbose_json with words")

// Diarize labels the words of an ASR lane's verbose_json body with the
// speakers of block.Segments (AttributeSpeakers, within maxGap) and adds
// "speakers", "utterances" and "diarization". Words come from the top-level
// "words" list, or are merged from whisper-server's per-segment tokens when
// the lane did not add them. Every other field of the body is kept.
func Diarize(body []byte, block DiarizationBlock, maxGap float64) ([]byte, Attribution, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, Attribution{}, fmt.Errorf("the transcription is not a JSON object: %w", err)
	}
	var words []Word
	if raw, ok := doc["words"]; ok {
		if err := json.Unmarshal(raw, &words); err != nil {
			return nil, Attribution{}, fmt.Errorf("the transcription's words: %w", err)
		}
	} else if merged, ok := whisperCppWords(doc["segments"]); ok {
		words = merged
	}
	if len(words) == 0 {
		return nil, Attribution{}, ErrNoWords
	}
	if block.Segments == nil {
		block.Segments = []Turn{}
	}
	labelled := AttributeSpeakers(words, block.Segments, maxGap)
	speakers := Speakers(block.Segments)
	if block.NumSpeakers == 0 {
		block.NumSpeakers = len(speakers)
	}
	for key, v := range map[string]any{
		"words":       words,
		"speakers":    speakers,
		"utterances":  Utterances(words),
		"diarization": block,
	} {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, Attribution{}, err
		}
		doc[key] = raw
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, Attribution{}, err
	}
	return out, Attribution{Words: len(words), Labelled: labelled}, nil
}
