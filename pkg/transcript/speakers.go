package transcript

import (
	"sort"
	"strings"
)

// Turn is one speaker turn from a diarization lane (pyannote's itertracks,
// an RTTM SPEAKER line). Turns may overlap: pyannote reports overlapped
// speech as two turns.
type Turn struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

// DefaultSpeakerGap is how far, in seconds, a word that no turn covers may be
// from the nearest turn and still take its speaker. Whisper's word timings
// and pyannote's turn edges disagree by a few hundred milliseconds at speaker
// changes; a word further out than this is left unlabelled.
const DefaultSpeakerGap = 1.0

// AttributeSpeakers labels each word with the speaker whose turns overlap it
// the longest; ties go to the speaker whose overlapping turn starts first. A
// word of zero duration takes the first turn that contains its instant. A
// word no turn covers takes the speaker of the nearest turn within maxGap
// seconds, or stays unlabelled. Words keep their order; it returns how many
// were labelled.
func AttributeSpeakers(words []Word, turns []Turn, maxGap float64) int {
	idx := newTurnIndex(turns)
	labelled := 0
	for i := range words {
		words[i].Speaker = ""
		if idx != nil {
			words[i].Speaker = idx.speakerFor(words[i].Start, words[i].End, maxGap)
		}
		if words[i].Speaker != "" {
			labelled++
		}
	}
	return labelled
}

// turnIndex holds turns sorted by start with the running maximum end, so a
// lookup skips every turn that ended before the word.
type turnIndex struct {
	turns  []Turn
	maxEnd []float64 // maxEnd[i] = max(turns[0..i].End)
	argMax []int     // argMax[i] = index of that maximum
}

func newTurnIndex(turns []Turn) *turnIndex {
	sorted := make([]Turn, 0, len(turns))
	for _, t := range turns {
		if t.Speaker == "" || t.End < t.Start {
			continue
		}
		sorted = append(sorted, t)
	}
	if len(sorted) == 0 {
		return nil
	}
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Start < sorted[b].Start })
	idx := &turnIndex{turns: sorted, maxEnd: make([]float64, len(sorted)), argMax: make([]int, len(sorted))}
	for i, t := range sorted {
		idx.maxEnd[i], idx.argMax[i] = t.End, i
		if i > 0 && idx.maxEnd[i-1] >= t.End {
			idx.maxEnd[i], idx.argMax[i] = idx.maxEnd[i-1], idx.argMax[i-1]
		}
	}
	return idx
}

func (x *turnIndex) speakerFor(start, end, maxGap float64) string {
	if end < start {
		start, end = end, start
	}
	instant := end == start
	// Every turn before first ended before the word (or, for an instant,
	// before it).
	first := sort.Search(len(x.turns), func(i int) bool {
		return x.maxEnd[i] > start || (instant && x.maxEnd[i] >= start)
	})
	overlap := map[string]float64{}
	var order []string
	for i := first; i < len(x.turns) && x.turns[i].Start <= end; i++ {
		t := x.turns[i]
		if instant {
			if t.Start <= start && start <= t.End {
				return t.Speaker
			}
			continue
		}
		o := min(end, t.End) - max(start, t.Start)
		if o <= 0 {
			continue
		}
		if _, seen := overlap[t.Speaker]; !seen {
			order = append(order, t.Speaker)
		}
		overlap[t.Speaker] += o
	}
	best, bestOverlap := "", 0.0
	for _, s := range order {
		if overlap[s] > bestOverlap {
			best, bestOverlap = s, overlap[s]
		}
	}
	if best != "" {
		return best
	}
	return x.nearest(start, end, maxGap)
}

// nearest is the speaker of the turn closest to [start, end] within maxGap;
// a tie goes to the turn before the word.
func (x *turnIndex) nearest(start, end, maxGap float64) string {
	best, bestGap := "", maxGap
	found := false
	// The turn ending last among those starting at or before start.
	if before := sort.Search(len(x.turns), func(i int) bool { return x.turns[i].Start > start }) - 1; before >= 0 {
		if gap := start - x.maxEnd[before]; gap >= 0 && gap <= bestGap {
			best, bestGap, found = x.turns[x.argMax[before]].Speaker, gap, true
		}
	}
	// The first turn starting at or after end.
	if after := sort.Search(len(x.turns), func(i int) bool { return x.turns[i].Start >= end }); after < len(x.turns) {
		if gap := x.turns[after].Start - end; gap >= 0 && (gap < bestGap || (!found && gap <= bestGap)) {
			best = x.turns[after].Speaker
		}
	}
	return best
}

// Utterance is a run of consecutive words by one speaker.
type Utterance struct {
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
}

// Utterances groups words into speaker runs. A run changes only between two
// different speaker labels; an unlabelled word joins the run it falls in.
func Utterances(words []Word) []Utterance {
	out := []Utterance{}
	var text []string
	flush := func() {
		if len(text) > 0 {
			out[len(out)-1].Text = strings.Join(text, " ")
			text = text[:0]
		}
	}
	for _, w := range words {
		if len(out) == 0 || (w.Speaker != "" && out[len(out)-1].Speaker != "" && w.Speaker != out[len(out)-1].Speaker) {
			flush()
			out = append(out, Utterance{Speaker: w.Speaker, Start: w.Start, End: w.End})
		}
		u := &out[len(out)-1]
		if u.Speaker == "" {
			u.Speaker = w.Speaker
		}
		u.End = max(u.End, w.End)
		text = append(text, w.Word)
	}
	flush()
	return out
}

// Speakers lists the distinct speaker labels of turns, sorted.
func Speakers(turns []Turn) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range turns {
		if t.Speaker != "" && !seen[t.Speaker] {
			seen[t.Speaker] = true
			out = append(out, t.Speaker)
		}
	}
	sort.Strings(out)
	return out
}
