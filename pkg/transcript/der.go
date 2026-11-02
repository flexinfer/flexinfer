package transcript

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ParseRTTM reads the SPEAKER lines of an RTTM file as turns. uri keeps only
// the lines of one recording (the file-id column); "" keeps every line. Lines
// of other types and ";;" comments are skipped.
func ParseRTTM(r io.Reader, uri string) ([]Turn, error) {
	var turns []Turn
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || strings.HasPrefix(f[0], ";;") || f[0] != "SPEAKER" {
			continue
		}
		if len(f) < 8 {
			return nil, fmt.Errorf("rttm line %d: want at least 8 fields, got %d", n, len(f))
		}
		if uri != "" && f[1] != uri {
			continue
		}
		start, err1 := strconv.ParseFloat(f[3], 64)
		dur, err2 := strconv.ParseFloat(f[4], 64)
		if err1 != nil || err2 != nil || dur < 0 {
			return nil, fmt.Errorf("rttm line %d: bad onset %q or duration %q", n, f[3], f[4])
		}
		turns = append(turns, Turn{Start: start, End: start + dur, Speaker: f[7]})
	}
	return turns, sc.Err()
}

// WriteRTTM writes turns as RTTM SPEAKER lines for recording uri.
func WriteRTTM(w io.Writer, uri string, turns []Turn) error {
	for _, t := range turns {
		if _, err := fmt.Fprintf(w, "SPEAKER %s 1 %.3f %.3f <NA> <NA> %s <NA> <NA>\n", uri, t.Start, t.End-t.Start, t.Speaker); err != nil {
			return err
		}
	}
	return nil
}

// Shift moves turns by offset seconds and clips them to [0, duration]; a
// non-positive duration leaves the end open. It cuts a recording's reference
// to the clip a kill-test sent (AMI EN2002a minutes 0-30, say).
func Shift(turns []Turn, offset, duration float64) []Turn {
	out := make([]Turn, 0, len(turns))
	for _, t := range turns {
		s, e := t.Start+offset, t.End+offset
		s = max(s, 0)
		if duration > 0 {
			e = min(e, duration)
		}
		if e > s {
			out = append(out, Turn{Start: s, End: e, Speaker: t.Speaker})
		}
	}
	return out
}

// TurnsFromUtterances are the speaker turns a diarized transcript's words
// imply: one per utterance with a speaker.
func TurnsFromUtterances(utterances []Utterance) []Turn {
	out := make([]Turn, 0, len(utterances))
	for _, u := range utterances {
		if u.Speaker != "" && u.End > u.Start {
			out = append(out, Turn{Start: u.Start, End: u.End, Speaker: u.Speaker})
		}
	}
	return out
}

// DER is a diarization error rate and its parts, in seconds of reference
// speech (overlapped speech counts once per speaker), as md-eval and
// pyannote.metrics define it: each hypothesis speaker maps to at most one
// reference speaker, by the assignment that maximises their total overlap.
type DER struct {
	Rate       float64           `json:"der"`
	Total      float64           `json:"total_seconds"`
	Missed     float64           `json:"missed_seconds"`
	FalseAlarm float64           `json:"false_alarm_seconds"`
	Confusion  float64           `json:"confusion_seconds"`
	Mapping    map[string]string `json:"mapping"`
}

// DEROptions bound the scoring.
type DEROptions struct {
	// Collar is excluded around every reference turn boundary, in seconds
	// each side (md-eval's --collar; pyannote.metrics' collar is twice it).
	Collar float64
	// Start and End bound the scored region; End 0 means the end of the last
	// reference turn.
	Start, End float64
}

// DiarizationErrorRate scores hyp against ref.
func DiarizationErrorRate(ref, hyp []Turn, opts DEROptions) DER {
	end := opts.End
	if end <= 0 {
		for _, t := range ref {
			end = max(end, t.End)
		}
	}
	// No-score zones around every reference boundary.
	var collars [][2]float64
	if opts.Collar > 0 {
		for _, t := range ref {
			for _, b := range []float64{t.Start, t.End} {
				collars = append(collars, [2]float64{b - opts.Collar, b + opts.Collar})
			}
		}
	}
	cuts := []float64{opts.Start, end}
	for _, ts := range [][]Turn{ref, hyp} {
		for _, t := range ts {
			cuts = append(cuts, t.Start, t.End)
		}
	}
	for _, c := range collars {
		cuts = append(cuts, c[0], c[1])
	}
	sort.Float64s(cuts)

	refIDs, hypIDs := speakerIndex(ref), speakerIndex(hyp)
	overlap := make([][]float64, len(refIDs.names))
	for i := range overlap {
		overlap[i] = make([]float64, len(hypIDs.names))
	}
	type piece struct {
		dur      float64
		ref, hyp []int
	}
	var pieces []piece
	for i := 0; i+1 < len(cuts); i++ {
		a, b := cuts[i], cuts[i+1]
		if b <= a || b <= opts.Start || a >= end {
			continue
		}
		a, b = max(a, opts.Start), min(b, end)
		mid := (a + b) / 2
		if inAny(collars, mid) {
			continue
		}
		p := piece{dur: b - a, ref: activeAt(ref, refIDs, mid), hyp: activeAt(hyp, hypIDs, mid)}
		for _, r := range p.ref {
			for _, h := range p.hyp {
				overlap[r][h] += p.dur
			}
		}
		pieces = append(pieces, p)
	}

	assign := maxWeightAssignment(overlap) // hyp index -> ref index or -1
	d := DER{Mapping: map[string]string{}}
	for h, r := range assign {
		if r >= 0 && overlap[r][h] > 0 {
			d.Mapping[hypIDs.names[h]] = refIDs.names[r]
		} else {
			assign[h] = -1
		}
	}
	for _, p := range pieces {
		nRef, nHyp := len(p.ref), len(p.hyp)
		correct := 0
		for _, h := range p.hyp {
			if r := assign[h]; r >= 0 && containsInt(p.ref, r) {
				correct++
			}
		}
		d.Total += p.dur * float64(nRef)
		d.Missed += p.dur * float64(max(nRef-nHyp, 0))
		d.FalseAlarm += p.dur * float64(max(nHyp-nRef, 0))
		d.Confusion += p.dur * float64(min(nRef, nHyp)-correct)
	}
	if d.Total > 0 {
		d.Rate = (d.Missed + d.FalseAlarm + d.Confusion) / d.Total
	}
	return d
}

type speakerIDs struct {
	names []string
	index map[string]int
}

func speakerIndex(turns []Turn) speakerIDs {
	ids := speakerIDs{index: map[string]int{}}
	for _, t := range turns {
		if _, ok := ids.index[t.Speaker]; !ok {
			ids.index[t.Speaker] = len(ids.names)
			ids.names = append(ids.names, t.Speaker)
		}
	}
	return ids
}

// activeAt lists the distinct speakers with a turn covering instant t.
func activeAt(turns []Turn, ids speakerIDs, t float64) []int {
	var out []int
	for _, turn := range turns {
		if turn.Start <= t && t < turn.End {
			if id := ids.index[turn.Speaker]; !containsInt(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

func inAny(zones [][2]float64, t float64) bool {
	for _, z := range zones {
		if z[0] <= t && t < z[1] {
			return true
		}
	}
	return false
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// maxWeightAssignment maps each column (hypothesis speaker) to at most one
// row (reference speaker) so the summed weight is largest: the Hungarian
// algorithm on the padded square cost matrix. It returns, per column, the
// row or -1.
func maxWeightAssignment(w [][]float64) []int {
	rows := len(w)
	cols := 0
	if rows > 0 {
		cols = len(w[0])
	}
	n := max(rows, cols)
	if n == 0 || cols == 0 {
		return make([]int, cols)
	}
	peak := 0.0
	for _, row := range w {
		for _, v := range row {
			peak = max(peak, v)
		}
	}
	cost := func(i, j int) float64 { // 1-based, padded with zero weight
		if i <= rows && j <= cols {
			return peak - w[i-1][j-1]
		}
		return peak
	}
	// e-maxx Hungarian, rows 1..n assigned to columns 1..n.
	u, v := make([]float64, n+1), make([]float64, n+1)
	p, way := make([]int, n+1), make([]int, n+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := range minv {
			minv[j] = math.Inf(1)
		}
		for {
			used[j0] = true
			i0, delta, j1 := p[j0], math.Inf(1), 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				if cur := cost(i0, j) - u[i0] - v[j]; cur < minv[j] {
					minv[j], way[j] = cur, j0
				}
				if minv[j] < delta {
					delta, j1 = minv[j], j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for j0 != 0 {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
		}
	}
	out := make([]int, cols)
	for j := 1; j <= cols; j++ {
		out[j-1] = -1
		if i := p[j]; i >= 1 && i <= rows {
			out[j-1] = i - 1
		}
	}
	return out
}
