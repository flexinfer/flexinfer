package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/flexinfer/flexinfer/pkg/transcript"
)

// diarize_conformance is the D5-a gate and the scorer of kill-test KT-DIAR
// (.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.5,
// plan .loom/32-iteration-plan-d5a-diarize-job-2026-11-01.md). It reads a
// diarize job's content (a diarized transcript), the standalone pyannote
// Deployment's /diarize answer for the same audio, and the reference RTTM,
// scores both against the reference (DER with optimal speaker mapping,
// pkg/transcript), checks the job's words carry speakers, and takes the gte
// p95 the window measured beside the job. It prints a JSON report and a
// ready-to-paste .loom/60-validation-matrix.md row, and exits 0 on PASS.
// It runs no inference: the job and the probe run in the window.

func init() { registerSubcommand("diarize_conformance", runDiarizeConformanceMain) }

func runDiarizeConformanceMain(args []string) int {
	return runDiarizeConformance(args, os.Stdout, os.Stderr)
}

// Pass rule, §6.5 verbatim: a 30-min AMI meeting through the job path,
// speaker-attributed words, DER within 5 points of the standalone pyannote
// Deployment, gte p95 unaffected.
const dcPassRule = "a 30-min AMI meeting through the job path, speaker-attributed words, DER within 5 points of the standalone pyannote Deployment, gte p95 unaffected"

type dcConfig struct {
	Reference       string
	URI             string
	RefOffset       float64
	Duration        float64
	Hypothesis      string
	Baseline        string
	Collar          float64
	MaxDERDelta     float64
	MinWordCoverage float64
	MinMinutes      float64
	GTEBaselineP95  float64
	GTERunP95       float64
	GTETolerance    float64
	Job             string
	Image           string
	Row             string
	Out             string
	Command         string
}

func parseDCFlags(args []string, stderr io.Writer) (*dcConfig, error) {
	fs := flag.NewFlagSet("diarize_conformance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := &dcConfig{}
	fs.StringVar(&cfg.Reference, "reference", "", "Reference RTTM (AMI only_words). Required.")
	fs.StringVar(&cfg.URI, "uri", "", "RTTM file id to score (EN2002a). Default: every SPEAKER line.")
	fs.Float64Var(&cfg.RefOffset, "ref-offset", 0, "Seconds added to reference times: minus the clip's start in the recording (-300 for a clip from 5:00).")
	fs.Float64Var(&cfg.Duration, "duration", 0, "Clip length in seconds; the scored region is [0, duration]. Default: the job transcript's duration.")
	fs.StringVar(&cfg.Hypothesis, "hypothesis", "", "The diarize job's content (GET /v1/jobs/{id}/content). Required.")
	fs.StringVar(&cfg.Baseline, "baseline", "", "The standalone pyannote Deployment's answer for the same clip: /diarize JSON or RTTM. Required.")
	fs.Float64Var(&cfg.Collar, "collar", 0.25, "No-score collar, seconds each side of every reference boundary (md-eval --collar).")
	fs.Float64Var(&cfg.MaxDERDelta, "max-der-delta", 5, "Pass when the job's DER exceeds the standalone Deployment's by at most this many points.")
	fs.Float64Var(&cfg.MinWordCoverage, "min-word-coverage", 0.95, "Share of the job's words that must carry a speaker for \"speaker-attributed words\".")
	fs.Float64Var(&cfg.MinMinutes, "min-minutes", 29.5, "Shortest scored clip that counts as the 30-min meeting.")
	fs.Float64Var(&cfg.GTEBaselineP95, "gte-baseline-p95-ms", 0, "gte p95 (ms) of the same window's baseline probe, before the job.")
	fs.Float64Var(&cfg.GTERunP95, "gte-run-p95-ms", 0, "gte p95 (ms) while the job ran.")
	fs.Float64Var(&cfg.GTETolerance, "gte-tolerance", 0.20, "\"unaffected\": run p95 at most this fraction above baseline (KT-ASR criterion d).")
	fs.StringVar(&cfg.Job, "job", "", "Job id, for the record.")
	fs.StringVar(&cfg.Image, "image", "", "Matrix row runtime_image (the gfx906 runtime with the pyannote layer).")
	fs.StringVar(&cfg.Row, "row", "", "Matrix row id. Default gfx906-diarize-job-<UTC date>.")
	fs.StringVar(&cfg.Out, "out", "", "Also write the JSON report to this file.")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	for name, v := range map[string]string{"--reference": cfg.Reference, "--hypothesis": cfg.Hypothesis, "--baseline": cfg.Baseline} {
		if v == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	if cfg.Row == "" {
		cfg.Row = "gfx906-diarize-job-" + time.Now().UTC().Format("2006-01-02")
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = a
		if strings.ContainsAny(a, " \t'\"") {
			quoted[i] = strconv.Quote(a)
		}
	}
	cfg.Command = strings.TrimSpace("flexinfer-bench diarize_conformance " + strings.Join(quoted, " "))
	return cfg, nil
}

// dcReport is the gate's JSON output.
type dcReport struct {
	Gate         string         `json:"gate"`
	Row          string         `json:"row"`
	Job          string         `json:"job,omitempty"`
	Command      string         `json:"command"`
	ScoredUTC    string         `json:"scored_utc"`
	Region       [2]float64     `json:"region_seconds"`
	Collar       float64        `json:"collar_seconds"`
	Words        int            `json:"words"`
	Labelled     int            `json:"labelled_words"`
	WordCoverage float64        `json:"word_coverage"`
	Speakers     []string       `json:"speakers"`
	JobDER       transcript.DER `json:"job_der"`
	WordsDER     transcript.DER `json:"words_der"`
	BaselineDER  transcript.DER `json:"baseline_der"`
	DeltaPoints  float64        `json:"der_delta_points"`
	Checks       []tccCheck     `json:"checks"`
	Pass         bool           `json:"pass"`
	Verdict      string         `json:"verdict"`
	MatrixRow    string         `json:"matrix_row"`
	PassRule     string         `json:"pass_rule"`
	Errors       []string       `json:"errors,omitempty"`
}

func runDiarizeConformance(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseDCFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "diarize_conformance:", err)
		return 2
	}
	report, err := scoreDiarization(cfg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "diarize_conformance:", err)
		return 2
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "diarize_conformance: marshal report:", err)
		return 2
	}
	_, _ = fmt.Fprintln(stdout, string(out))
	if cfg.Out != "" {
		if err := os.WriteFile(cfg.Out, append(out, '\n'), 0o644); err != nil {
			_, _ = fmt.Fprintln(stderr, "diarize_conformance: write --out:", err)
		}
	}
	_, _ = fmt.Fprintf(stderr, "\n%s — matrix row:\n%s\n", report.Verdict, report.MatrixRow)
	if !report.Pass {
		return 1
	}
	return 0
}

// scoreDiarization reads the three inputs and evaluates the pass rule.
func scoreDiarization(cfg *dcConfig) (*dcReport, error) {
	refFile, err := os.Open(cfg.Reference)
	if err != nil {
		return nil, err
	}
	refAll, err := transcript.ParseRTTM(refFile, cfg.URI)
	_ = refFile.Close()
	if err != nil {
		return nil, fmt.Errorf("reference: %w", err)
	}
	raw, err := os.ReadFile(cfg.Hypothesis)
	if err != nil {
		return nil, err
	}
	var hyp transcript.Diarized
	if err := json.Unmarshal(raw, &hyp); err != nil {
		return nil, fmt.Errorf("hypothesis: not a diarized transcript: %w", err)
	}
	baseline, err := readTurns(cfg.Baseline, cfg.URI)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}

	duration := cfg.Duration
	if duration <= 0 {
		duration = hyp.Duration
	}
	ref := transcript.Shift(refAll, cfg.RefOffset, duration)
	if len(ref) == 0 {
		return nil, errors.New("reference: no turns in the scored region; check --uri, --ref-offset and --duration")
	}
	if duration <= 0 {
		for _, t := range ref {
			duration = max(duration, t.End)
		}
	}
	opts := transcript.DEROptions{Collar: cfg.Collar, End: duration}

	r := &dcReport{
		Gate: "diarize_conformance", Row: cfg.Row, Job: cfg.Job, Command: cfg.Command,
		ScoredUTC: time.Now().UTC().Format(time.RFC3339), Region: [2]float64{0, duration}, Collar: cfg.Collar,
		Words: len(hyp.Words), Speakers: hyp.Speakers, PassRule: dcPassRule,
	}
	for _, w := range hyp.Words {
		if w.Speaker != "" {
			r.Labelled++
		}
	}
	if r.Words > 0 {
		r.WordCoverage = float64(r.Labelled) / float64(r.Words)
	}
	r.JobDER = transcript.DiarizationErrorRate(ref, hyp.Diarization.Segments, opts)
	r.WordsDER = transcript.DiarizationErrorRate(ref, transcript.TurnsFromUtterances(hyp.Utterances), opts)
	r.BaselineDER = transcript.DiarizationErrorRate(ref, baseline, opts)
	r.DeltaPoints = 100 * (r.JobDER.Rate - r.BaselineDER.Rate)
	r.Checks = dcEvaluate(cfg, r, duration)
	r.Verdict, r.Pass = "PASS", true
	for _, c := range r.Checks {
		switch {
		case c.Status == "fail":
			r.Verdict, r.Pass = "FAIL", false
		case c.Status == "n/a" && r.Verdict == "PASS":
			r.Verdict, r.Pass = "INCOMPLETE", false
		}
	}
	r.MatrixRow = dcMatrixRow(cfg, r)
	return r, nil
}

// readTurns reads speaker turns from a /diarize or diarized-transcript JSON
// answer, or from an RTTM file.
func readTurns(path, uri string) ([]transcript.Turn, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' {
		var doc struct {
			Segments    []transcript.Turn `json:"segments"`
			Diarization *struct {
				Segments []transcript.Turn `json:"segments"`
			} `json:"diarization"`
		}
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, err
		}
		if doc.Diarization != nil {
			return doc.Diarization.Segments, nil
		}
		return doc.Segments, nil
	}
	return transcript.ParseRTTM(bytes.NewReader(raw), uri)
}

func dcEvaluate(cfg *dcConfig, r *dcReport, duration float64) []tccCheck {
	pct := func(v float64) string { return strconv.FormatFloat(100*v, 'f', 2, 64) + " %" }
	status := func(ok bool) string {
		if ok {
			return "pass"
		}
		return "fail"
	}
	checks := []tccCheck{
		{
			Name: "meeting_length", Status: status(duration >= 60*cfg.MinMinutes),
			Value: fmt.Sprintf("%.1f min", duration/60), Threshold: fmt.Sprintf(">= %.1f min", cfg.MinMinutes),
		},
		{
			Name: "speaker_attributed_words", Status: status(r.Words > 0 && r.WordCoverage >= cfg.MinWordCoverage),
			Value: fmt.Sprintf("%d/%d words (%s)", r.Labelled, r.Words, pct(r.WordCoverage)), Threshold: ">= " + pct(cfg.MinWordCoverage),
		},
		{
			Name: "der_vs_standalone", Status: status(r.DeltaPoints <= cfg.MaxDERDelta),
			Value:     fmt.Sprintf("job %s vs standalone %s (%+.2f points)", pct(r.JobDER.Rate), pct(r.BaselineDER.Rate), r.DeltaPoints),
			Threshold: fmt.Sprintf("<= +%.1f points", cfg.MaxDERDelta),
			Detail:    fmt.Sprintf("collar %.2f s; words-derived DER %s", cfg.Collar, pct(r.WordsDER.Rate)),
		},
	}
	gte := tccCheck{Name: "gte_p95", Status: "n/a", Threshold: fmt.Sprintf("run <= baseline x %.2f", 1+cfg.GTETolerance),
		Detail: "pass --gte-baseline-p95-ms and --gte-run-p95-ms from the window's probe"}
	if cfg.GTEBaselineP95 > 0 && cfg.GTERunP95 > 0 {
		ratio := cfg.GTERunP95 / cfg.GTEBaselineP95
		gte.Status = status(ratio <= 1+cfg.GTETolerance)
		gte.Value = fmt.Sprintf("%.1f -> %.1f ms (%+.0f %%)", cfg.GTEBaselineP95, cfg.GTERunP95, 100*(ratio-1))
		gte.Detail = ""
	}
	return append(checks, gte)
}

func dcMatrixRow(cfg *dcConfig, r *dcReport) string {
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") }
	var ev strings.Builder
	fmt.Fprintf(&ev, "**%s** (scored %s). Job DER %.2f %% (missed %.0f s, false alarm %.0f s, confusion %.0f s of %.0f s), standalone %.2f %%, delta %+.2f points; words-derived DER %.2f %%; %d/%d words labelled; %d speakers",
		r.Verdict, r.ScoredUTC, 100*r.JobDER.Rate, r.JobDER.Missed, r.JobDER.FalseAlarm, r.JobDER.Confusion, r.JobDER.Total,
		100*r.BaselineDER.Rate, r.DeltaPoints, 100*r.WordsDER.Rate, r.Labelled, r.Words, len(r.Speakers))
	for _, c := range r.Checks {
		fmt.Fprintf(&ev, "; %s %s", c.Name, c.Status)
		if c.Value != "" {
			fmt.Fprintf(&ev, " %s", c.Value)
		}
	}
	failure := "none"
	if !r.Pass {
		var fails []string
		for _, c := range r.Checks {
			if c.Status != "pass" {
				fails = append(fails, c.Name+" "+c.Status)
			}
		}
		failure = strings.Join(fails, "; ")
	}
	decision := "`fail`"
	switch r.Verdict {
	case "PASS":
		decision = "`promote` (set the `pyannote/speaker-diarization` certificate on `deploy/gpuprofiles/gfx906.yaml` for this image)"
	case "INCOMPLETE":
		decision = "`pending` (gte p95 not given)"
	}
	image := "TBD: pass --image"
	if cfg.Image != "" {
		image = "`" + cfg.Image + "`"
	}
	job := ""
	if r.Job != "" {
		job = ", job `" + r.Job + "`"
	}
	cells := []string{
		fmt.Sprintf("`%s` — KT-DIAR, feature D5-a (meeting pipeline) on `pyannote-diarization-radeonvii` behind `whisper-large-v3-turbo-radeonvii` via `flexinfer-bench diarize_conformance`%s", r.Row, job),
		"n/a (audio)",
		"`gfx906/radeonvii`",
		"`pyannote` (pyannote.audio 3.3.2, speaker-diarization-3.1) after `whispercpp`, as a D2 diarize job",
		"`experimental` (window canary)",
		image,
		"`HF://pyannote/speaker-diarization-3.1`",
		ev.String(),
		failure,
		"`" + r.Command + "`",
		"Delete `pyannote-diarization-radeonvii`, drop the provisional certificate annotations; the standalone Deployment keeps serving `/diarize`",
		"`.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` §6.5; `.loom/32-iteration-plan-d5a-diarize-job-2026-11-01.md`",
		decision,
	}
	for i := range cells {
		cells[i] = cell(cells[i])
	}
	return "| " + strings.Join(cells, " | ") + " |"
}
