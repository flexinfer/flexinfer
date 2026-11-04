package transcript

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func w(word string, start, end float64) Word { return Word{Word: word, Start: start, End: end} }

func speakersOf(words []Word) []string {
	out := make([]string, len(words))
	for i, word := range words {
		out[i] = word.Speaker
	}
	return out
}

func TestAttributeSpeakers(t *testing.T) {
	turns := []Turn{
		{Start: 5.0, End: 9.0, Speaker: "B"}, // unsorted on purpose
		{Start: 0.0, End: 5.2, Speaker: "A"},
		{Start: 8.5, End: 9.5, Speaker: "A"}, // overlapped speech
		{Start: 20.0, End: 25.0, Speaker: "C"},
	}
	words := []Word{
		w("hello", 0.1, 0.5),     // inside A
		w("there", 4.9, 5.4),     // A 0.3 s, B 0.4 s -> B
		w("ok", 5.1, 5.2),        // A and B tie at 0.1 s -> the turn starting first (A)
		w("yes", 8.6, 9.2),       // B 0.4 s + A 0.6 s -> A
		w("um", 9.6, 9.7),        // no turn; 0.1 s after A's end
		w("so", 15.0, 15.2),      // no turn within 1 s
		w("right", 19.3, 19.6),   // no turn; 0.4 s before C
		w(".", 22.0, 22.0),       // instant inside C
		w("gone", 30.0, 31.0),    // after every turn
		w("bad", 3.0, 2.5),       // inverted timings are swapped, inside A
		w("edge", 25.0, 25.5),    // touches C's end: gap 0
		w("instant", 25.6, 25.6), // instant in no turn, 0.6 s after C
	}
	n := AttributeSpeakers(words, turns, DefaultSpeakerGap)
	assert.Equal(t, []string{"A", "B", "A", "A", "A", "", "C", "C", "", "A", "C", "C"}, speakersOf(words))
	assert.Equal(t, 10, n)

	assert.Equal(t, 0, AttributeSpeakers(words, nil, DefaultSpeakerGap), "no turns clears the labels")
	assert.Equal(t, make([]string, len(words)), speakersOf(words))
}

func TestAttributeSpeakersScalesToAMeeting(t *testing.T) {
	// 60 minutes, ~10,600 words, a turn change every ~8 s.
	var turns []Turn
	for s := 0.0; s < 3600; s += 8 {
		turns = append(turns, Turn{Start: s, End: s + 8.2, Speaker: []string{"A", "B", "C", "D"}[int(s/8)%4]})
	}
	words := make([]Word, 0, 10600)
	for s := 0.0; s < 3600; s += 0.34 {
		words = append(words, w("x", s, s+0.3))
	}
	assert.Equal(t, len(words), AttributeSpeakers(words, turns, DefaultSpeakerGap))
	at := 9.0
	assert.Equal(t, "B", words[int(at/0.34)].Speaker)
}

func TestUtterances(t *testing.T) {
	words := []Word{
		{Word: "Hi", Start: 0, End: 0.3},
		{Word: "all.", Start: 0.3, End: 0.6, Speaker: "A"},
		{Word: "Thanks,", Start: 1.0, End: 1.4, Speaker: "B"},
		{Word: "uh", Start: 1.4, End: 1.5},
		{Word: "great.", Start: 1.5, End: 2.0, Speaker: "B"},
		{Word: "Yes.", Start: 2.2, End: 2.5, Speaker: "A"},
	}
	assert.Equal(t, []Utterance{
		{Speaker: "A", Start: 0, End: 0.6, Text: "Hi all."},
		{Speaker: "B", Start: 1.0, End: 2.0, Text: "Thanks, uh great."},
		{Speaker: "A", Start: 2.2, End: 2.5, Text: "Yes."},
	}, Utterances(words))
	assert.Equal(t, []Utterance{}, Utterances(nil))
}

func TestSpeakers(t *testing.T) {
	assert.Equal(t, []string{"SPEAKER_00", "SPEAKER_01"}, Speakers([]Turn{{Speaker: "SPEAKER_01"}, {Speaker: "SPEAKER_00"}, {Speaker: "SPEAKER_01"}, {}}))
	assert.Equal(t, []string{}, Speakers(nil))
}

const verboseWithWords = `{"task":"transcribe","language":"english","duration":3.0,"text":" Hi all. Thanks.",
"segments":[{"id":0,"start":0,"end":3,"text":" Hi all. Thanks."}],
"words":[{"word":"Hi","start":0.1,"end":0.3,"probability":0.9},{"word":"all.","start":0.3,"end":0.7,"probability":0.8},
{"word":"Thanks.","start":1.6,"end":2.2}]}`

func TestDiarize(t *testing.T) {
	block := DiarizationBlock{Model: "pyannote-diarization-radeonvii", Pipeline: "pyannote/speaker-diarization-3.1",
		Segments: []Turn{{Start: 0, End: 1.0, Speaker: "SPEAKER_00"}, {Start: 1.5, End: 3.0, Speaker: "SPEAKER_01"}}}
	out, stats, err := Diarize([]byte(verboseWithWords), block, DefaultSpeakerGap)
	require.NoError(t, err)
	assert.Equal(t, Attribution{Words: 3, Labelled: 3}, stats)

	var got Diarized
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, " Hi all. Thanks.", got.Text)
	assert.Equal(t, 3.0, got.Duration)
	assert.Equal(t, []string{"SPEAKER_00", "SPEAKER_00", "SPEAKER_01"}, speakersOf(got.Words))
	require.NotNil(t, got.Words[0].Probability)
	assert.Nil(t, got.Words[2].Probability, "a word without a probability keeps none")
	assert.Equal(t, []string{"SPEAKER_00", "SPEAKER_01"}, got.Speakers)
	assert.Equal(t, []Utterance{
		{Speaker: "SPEAKER_00", Start: 0.1, End: 0.7, Text: "Hi all."},
		{Speaker: "SPEAKER_01", Start: 1.6, End: 2.2, Text: "Thanks."},
	}, got.Utterances)
	assert.Equal(t, 2, got.Diarization.NumSpeakers)
	assert.Equal(t, block.Segments, got.Diarization.Segments)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &raw))
	for _, kept := range []string{"task", "language", "segments"} {
		assert.Contains(t, raw, kept, "the ASR body's fields are kept")
	}
}

func TestDiarizeMergesWhisperCppTokensWhenTheLaneDidNot(t *testing.T) {
	body := `{"text":" Rade on","segments":[{"words":[{"word":" Rade","start":0.0,"end":0.4,"probability":0.5},{"word":"on","start":0.4,"end":0.7,"probability":0.7}]}]}`
	out, stats, err := Diarize([]byte(body), DiarizationBlock{Model: "d", Segments: []Turn{{Start: 0, End: 1, Speaker: "S"}}}, DefaultSpeakerGap)
	require.NoError(t, err)
	assert.Equal(t, Attribution{Words: 1, Labelled: 1}, stats)
	assert.Contains(t, string(out), `{"word":"Radeon","start":0,"end":0.7,"probability":0.6,"speaker":"S"}`)
}

func TestDiarizeRefusesBodiesWithoutWords(t *testing.T) {
	for name, body := range map[string]string{
		"plain json":    `{"text":"hello"}`,
		"empty words":   `{"text":"","words":[]}`,
		"not an object": `"hello"`,
	} {
		_, _, err := Diarize([]byte(body), DiarizationBlock{}, DefaultSpeakerGap)
		assert.Error(t, err, name)
	}
	_, _, err := Diarize([]byte(`{"text":"x"}`), DiarizationBlock{}, DefaultSpeakerGap)
	assert.ErrorIs(t, err, ErrNoWords)
	assert.True(t, strings.Contains(ErrNoWords.Error(), "verbose_json"))
}
