package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/flexinfer/flexinfer/pkg/transcript"
)

// dcMeeting is a synthetic meeting: speakers A, B and C take turns of
// turnSeconds for minutes.
func dcMeeting(minutes int, turnSeconds float64) []transcript.Turn {
	var turns []transcript.Turn
	names := []string{"A", "B", "C"}
	for i, s := 0, 0.0; s < float64(minutes*60); i, s = i+1, s+turnSeconds {
		turns = append(turns, transcript.Turn{Start: s, End: min(s+turnSeconds, float64(minutes*60)), Speaker: names[i%3]})
	}
	return turns
}

// dcHypothesis relabels the reference as a diarizer would (SPEAKER_0x) and
// moves every inner turn boundary late by lag seconds.
func dcHypothesis(ref []transcript.Turn, lag float64) []transcript.Turn {
	labels := map[string]string{"A": "SPEAKER_01", "B": "SPEAKER_00", "C": "SPEAKER_02"}
	out := make([]transcript.Turn, len(ref))
	for i, t := range ref {
		out[i] = transcript.Turn{Start: t.Start, End: t.End, Speaker: labels[t.Speaker]}
		if i > 0 {
			out[i].Start += lag
		}
		if i < len(ref)-1 {
			out[i].End += lag
		}
	}
	return out
}

// dcJob is a diarize job's content for turns: a word every 0.5 s, labelled
// through the same attribution the proxy uses unless unlabelled.
func dcJob(t *testing.T, turns []transcript.Turn, seconds float64, unlabelled bool) []byte {
	t.Helper()
	var words []transcript.Word
	for s := 0.1; s+0.4 < seconds; s += 0.5 {
		words = append(words, transcript.Word{Word: "w", Start: s, End: s + 0.4})
	}
	if !unlabelled {
		transcript.AttributeSpeakers(words, turns, transcript.DefaultSpeakerGap)
	}
	doc := transcript.Diarized{
		Text: "…", Duration: seconds, Words: words, Speakers: transcript.Speakers(turns),
		Utterances:  transcript.Utterances(words),
		Diarization: transcript.DiarizationBlock{Model: "pyannote-diarization-radeonvii", Pipeline: "pyannote/speaker-diarization-3.1", NumSpeakers: 3, Segments: turns},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	return raw
}

type dcFixture struct {
	dir, reference, hypothesis, baseline string
}

// newDCFixture writes a 30-minute meeting cut from 5:00 of recording
// EN2002a: the reference RTTM (in recording time), the standalone
// Deployment's /diarize answer (boundaries 0.5 s late) and the job's content.
func newDCFixture(t *testing.T, minutes int, job []transcript.Turn, unlabelled bool) dcFixture {
	t.Helper()
	dir := t.TempDir()
	ref := dcMeeting(minutes, 60)
	var rttm bytes.Buffer
	require.NoError(t, transcript.WriteRTTM(&rttm, "EN2002a", transcript.Shift(ref, 300, 0)))
	require.NoError(t, transcript.WriteRTTM(&rttm, "EN2002b", []transcript.Turn{{Start: 0, End: 9999, Speaker: "noise"}}))
	f := dcFixture{dir: dir, reference: filepath.Join(dir, "EN2002a.rttm"), hypothesis: filepath.Join(dir, "job.json"), baseline: filepath.Join(dir, "baseline.json")}
	require.NoError(t, os.WriteFile(f.reference, rttm.Bytes(), 0o644))
	base, err := json.Marshal(map[string]any{"segments": dcHypothesis(ref, 0.5), "num_speakers": 3})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.baseline, base, 0o644))
	if job == nil {
		job = dcHypothesis(ref, 1.0)
	}
	require.NoError(t, os.WriteFile(f.hypothesis, dcJob(t, job, float64(minutes*60), unlabelled), 0o644))
	return f
}

func (f dcFixture) args(extra ...string) []string {
	return append([]string{
		"--reference", f.reference, "--uri", "EN2002a", "--ref-offset", "-300",
		"--hypothesis", f.hypothesis, "--baseline", f.baseline, "--job", "job-0123",
		"--row", "gfx906-diarize-job-2026-11-06",
	}, extra...)
}

func runDC(t *testing.T, args []string) (int, dcReport, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runDiarizeConformance(args, &stdout, &stderr)
	var r dcReport
	if code != 2 {
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &r), stderr.String())
	}
	return code, r, stderr.String()
}

func dcCheck(r dcReport, name string) tccCheck {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return tccCheck{}
}

const dcMatrixColumns = 13 // .loom/60-validation-matrix.md Promotion Matrix header

func TestDiarizeConformancePassesAFaithfulJob(t *testing.T) {
	f := newDCFixture(t, 30, nil, false)
	code, r, stderr := runDC(t, f.args("--gte-baseline-p95-ms", "64.1", "--gte-run-p95-ms", "66.0", "--image", "registry.harbor.lan/flexinfer/runtime@sha256:abc"))
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "PASS", r.Verdict)
	for _, c := range r.Checks {
		assert.Equal(t, "pass", c.Status, c.Name)
	}
	// 29 inner boundaries, each 0.5 s (baseline) or 1.0 s (job) late, less
	// the 0.25 s collar: 7.25 s and 21.75 s of confusion in 1,800 s minus
	// the collars.
	assert.InDelta(t, 7.25, r.BaselineDER.Confusion, 1e-6)
	assert.InDelta(t, 21.75, r.JobDER.Confusion, 1e-6)
	assert.Zero(t, r.JobDER.Missed+r.JobDER.FalseAlarm)
	assert.InDelta(t, 100*(21.75-7.25)/r.JobDER.Total, r.DeltaPoints, 1e-6)
	assert.Equal(t, map[string]string{"SPEAKER_01": "A", "SPEAKER_00": "B", "SPEAKER_02": "C"}, r.JobDER.Mapping)
	assert.Equal(t, [2]float64{0, 1800}, r.Region, "the scored region is the clip, not the recording")
	assert.Equal(t, 1.0, r.WordCoverage)
	assert.Equal(t, dcPassRule, r.PassRule)

	assert.Equal(t, dcMatrixColumns, strings.Count(r.MatrixRow, " | ")+1, "a row with another cell count fails certify-matrix")
	assert.Contains(t, r.MatrixRow, "`gfx906-diarize-job-2026-11-06` — KT-DIAR")
	assert.Contains(t, r.MatrixRow, "`promote`")
	assert.Contains(t, r.MatrixRow, "job `job-0123`")
	assert.Contains(t, stderr, "PASS — matrix row:")
}

func TestDiarizeConformanceFailsAJobWorseThanTheDeployment(t *testing.T) {
	ref := dcMeeting(30, 60)
	worse := dcHypothesis(ref, 1.0)
	for i := 10; i < 20; i++ { // ten minutes of B labelled as C
		if worse[i].Speaker == "SPEAKER_00" {
			worse[i].Speaker = "SPEAKER_02"
		}
	}
	f := newDCFixture(t, 30, worse, false)
	code, r, _ := runDC(t, f.args("--gte-baseline-p95-ms", "64", "--gte-run-p95-ms", "64"))
	assert.Equal(t, 1, code)
	assert.Equal(t, "FAIL", r.Verdict)
	assert.Equal(t, "fail", dcCheck(r, "der_vs_standalone").Status)
	assert.Greater(t, r.DeltaPoints, 5.0)
	assert.Contains(t, r.MatrixRow, "der_vs_standalone fail")
	assert.Contains(t, r.MatrixRow, "`fail`")
}

func TestDiarizeConformanceOtherChecks(t *testing.T) {
	t.Run("gte p95 not given is incomplete", func(t *testing.T) {
		f := newDCFixture(t, 30, nil, false)
		code, r, _ := runDC(t, f.args())
		assert.Equal(t, 1, code)
		assert.Equal(t, "INCOMPLETE", r.Verdict)
		assert.Equal(t, "n/a", dcCheck(r, "gte_p95").Status)
		assert.Contains(t, r.MatrixRow, "`pending`")
	})
	t.Run("gte p95 moved", func(t *testing.T) {
		f := newDCFixture(t, 30, nil, false)
		code, r, _ := runDC(t, f.args("--gte-baseline-p95-ms", "64.1", "--gte-run-p95-ms", "88.9"))
		assert.Equal(t, 1, code)
		assert.Equal(t, "fail", dcCheck(r, "gte_p95").Status, "KT-ASR run 1's +38 % fails")
	})
	t.Run("words without speakers", func(t *testing.T) {
		f := newDCFixture(t, 30, nil, true)
		code, r, _ := runDC(t, f.args("--gte-baseline-p95-ms", "64", "--gte-run-p95-ms", "64"))
		assert.Equal(t, 1, code)
		assert.Equal(t, "fail", dcCheck(r, "speaker_attributed_words").Status)
	})
	t.Run("a ten-minute clip is not the 30-minute meeting", func(t *testing.T) {
		f := newDCFixture(t, 10, nil, false)
		code, r, _ := runDC(t, f.args("--gte-baseline-p95-ms", "64", "--gte-run-p95-ms", "64"))
		assert.Equal(t, 1, code)
		assert.Equal(t, "fail", dcCheck(r, "meeting_length").Status)
	})
	t.Run("an RTTM baseline scores like its JSON", func(t *testing.T) {
		f := newDCFixture(t, 30, nil, false)
		_, fromJSON, _ := runDC(t, f.args("--gte-baseline-p95-ms", "64", "--gte-run-p95-ms", "64"))
		var rttm bytes.Buffer
		require.NoError(t, transcript.WriteRTTM(&rttm, "EN2002a", dcHypothesis(dcMeeting(30, 60), 0.5)))
		f.baseline = filepath.Join(f.dir, "baseline.rttm")
		require.NoError(t, os.WriteFile(f.baseline, rttm.Bytes(), 0o644))
		_, fromRTTM, _ := runDC(t, f.args("--gte-baseline-p95-ms", "64", "--gte-run-p95-ms", "64"))
		assert.InDelta(t, fromJSON.BaselineDER.Rate, fromRTTM.BaselineDER.Rate, 1e-6)
	})
	t.Run("usage errors exit 2", func(t *testing.T) {
		code, _, stderr := runDC(t, []string{"--hypothesis", "x.json"})
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "is required")
		f := newDCFixture(t, 30, nil, false)
		code, _, stderr = runDC(t, f.args("--uri", "ES2004a"))
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "no turns in the scored region")
	})
}

// TestDiarizeConformanceCheckedInFixtures scores the small hand-written
// fixtures in testdata/diarize: a two-minute, three-speaker clip whose
// expected DERs are worked out in testdata/diarize/README.md.
func TestDiarizeConformanceCheckedInFixtures(t *testing.T) {
	dir := filepath.Join("testdata", "diarize")
	code, r, stderr := runDC(t, []string{
		"--reference", filepath.Join(dir, "reference.rttm"), "--uri", "clip",
		"--hypothesis", filepath.Join(dir, "job.json"), "--baseline", filepath.Join(dir, "baseline.json"),
		"--collar", "0", "--min-minutes", "2", "--gte-baseline-p95-ms", "60", "--gte-run-p95-ms", "61",
	})
	require.Equal(t, 0, code, stderr)
	assert.InDelta(t, 3.0/120, r.JobDER.Rate, 1e-9)
	assert.InDelta(t, 1.0/120, r.BaselineDER.Rate, 1e-9)
	assert.InDelta(t, 2.0/120*100, r.DeltaPoints, 1e-9)
	assert.Equal(t, 6, r.Words)
	assert.Equal(t, 6, r.Labelled)
	assert.InDelta(t, 2.0, r.JobDER.Missed, 1e-9)
	assert.InDelta(t, 1.0, r.JobDER.Confusion, 1e-9)
}
