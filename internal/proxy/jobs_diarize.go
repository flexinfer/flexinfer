package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync/atomic"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/transcript"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"k8s.io/apimachinery/pkg/api/meta"
)

// Diarize jobs: the meeting pipeline of D5-a
// (.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.5).
// A transcription that asks for speakers (diarize=true, or "speaker" in
// timestamp_granularities[]) is always a job, background or not: its runner
// sends the audio to the ASR lane for verbose_json words, then to the ASR
// lane's diarization lane (a pyannote Model, flexinfer.ai/diarization-model)
// for speaker turns, labels each word with a speaker (pkg/transcript) and
// stores the result through the job store. Each step is an ordinary forward
// through this proxy's handler, with the lane's cold start, budget, breaker
// and retries; a retry resumes at the step that failed. A transcription
// without those fields never reaches this file.

const (
	pathAudioTranscriptions = "/v1/audio/transcriptions"
	// pathAudioDiarizations is backend.PyannoteDiarizationPath.
	pathAudioDiarizations = "/v1/audio/diarizations"
	backendPyannote       = "pyannote"

	jobAnnotationPipeline         = "flexinfer.ai/job-pipeline"
	jobAnnotationDiarizationModel = "flexinfer.ai/job-diarization-model"
	jobPipelineDiarize            = "diarize"

	formDiarize              = "diarize"
	formDiarizationModel     = "diarization_model"
	formTimestampGranularity = "timestamp_granularities[]"

	// codeTranscriptUnusable is the runner-recorded error of a diarize job
	// whose ASR answer carries no word timings to attribute.
	codeTranscriptUnusable = "transcript_unusable"

	// jobProgressTranscribed is a diarize job's progress once its
	// transcription is done and the diarization lane is next.
	jobProgressTranscribed = 60
)

// diarizeSpeakerFields are the pyannote speaker-count fields a diarize job
// passes to the diarization lane and keeps from the ASR lane.
var diarizeSpeakerFields = []string{"num_speakers", "min_speakers", "max_speakers"}

// diarizeFormats are the response_format values a diarize job accepts. It
// always answers a diarized verbose_json document.
var diarizeFormats = map[string]bool{"": true, "json": true, "verbose_json": true, "diarized_json": true}

// multipartFields reads the non-file fields of a multipart body.
func multipartFields(raw []byte, boundary string) (map[string][]string, error) {
	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	fields := map[string][]string{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return fields, nil
		}
		if err != nil {
			return nil, err
		}
		if part.FileName() != "" {
			continue
		}
		value, err := io.ReadAll(io.LimitReader(part, 64<<10))
		if err != nil {
			return nil, err
		}
		fields[part.FormName()] = append(fields[part.FormName()], strings.TrimSpace(string(value)))
	}
}

func firstField(fields map[string][]string, name string) string {
	if v := fields[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// wantsDiarization reports diarize=true, or "speaker" among the timestamp
// granularities.
func wantsDiarization(fields map[string][]string) bool {
	switch strings.ToLower(firstField(fields, formDiarize)) {
	case "true", "1":
		return true
	}
	for _, name := range []string{formTimestampGranularity, "timestamp_granularities"} {
		for _, g := range fields[name] {
			if strings.EqualFold(g, "speaker") {
				return true
			}
		}
	}
	return false
}

// rewriteMultipart re-encodes a multipart body with the same boundary,
// keeping the parts keep accepts byte for byte and appending extra fields.
func rewriteMultipart(raw []byte, boundary string, keep func(name string) bool, extra [][2]string) ([]byte, error) {
	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, err
	}
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading multipart body: %w", err)
		}
		if !keep(part.FormName()) {
			continue
		}
		pw, err := writer.CreatePart(textproto.MIMEHeader(part.Header))
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(pw, part); err != nil {
			return nil, err
		}
	}
	for _, field := range extra {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ── submission ───────────────────────────────────────────────────

// maybeDiarizeJob answers a transcription that asks for speakers with a
// diarize job, or with the reason it cannot have one, and reports whether it
// answered. Any other request finds r.Body exactly as it arrived.
func (p *Proxy) maybeDiarizeJob(w http.ResponseWriter, r *http.Request) bool {
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	boundary := params["boundary"]
	if mediaType != "multipart/form-data" || boundary == "" {
		return false
	}
	original := r.Body
	raw, err := io.ReadAll(io.LimitReader(original, maxJobSubmitBytes+1))
	if err != nil || len(raw) > maxJobSubmitBytes {
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(raw), original), original}
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	fields, err := multipartFields(raw, boundary)
	if err != nil || !wantsDiarization(fields) {
		return false
	}

	if !p.jobsGate(w, r) {
		return true
	}
	model := r.Header.Get("X-Model-ID")
	if model == "" {
		model = firstField(fields, "model")
	}
	if model == "" {
		validation.WriteBadRequestWithCode(w, "X-Model-ID header or 'model' field required", validation.CodeMissingRequiredField)
		return true
	}
	if v := strings.ToLower(firstField(fields, "stream")); v == "true" || v == "1" {
		validation.WriteInvalidFieldValue(w, "stream", "diarize=true runs as a job and cannot stream; read the job's content")
		return true
	}
	if !diarizeFormats[firstField(fields, "response_format")] {
		validation.WriteInvalidFieldValue(w, "response_format", "diarize=true answers diarized verbose_json; use json, verbose_json or diarized_json")
		return true
	}
	if !p.checkJobTarget(w, r, model, pathAudioTranscriptions) {
		return true
	}
	name, lane, reason := p.diarizationLane(r.Context(), p.jobLaneModel(r.Context(), model), firstField(fields, formDiarizationModel))
	if reason != "" {
		p.writeDiarizationNotCertified(w, model, lane, reason)
		return true
	}
	body, err := rewriteMultipart(raw, boundary, func(field string) bool {
		return field != "background" && field != formDiarize && field != formDiarizationModel
	}, nil)
	if err != nil {
		validation.WriteBadRequest(w, err.Error())
		return true
	}
	sub := jobSubmission{
		model: model, path: pathAudioTranscriptions, body: body, contentType: r.Header.Get("Content-Type"),
		idempotencyKey: r.Header.Get(headerIdempotencyKey), qosClass: r.Header.Get(headerQoS),
		annotations: map[string]string{jobAnnotationPipeline: jobPipelineDiarize, jobAnnotationDiarizationModel: name},
	}
	if u := r.Header.Get(headerJobWebhookURL); u != "" {
		sub.webhook = &jobWebhookIn{URL: u, Secret: r.Header.Get(headerJobWebhookSecret)}
	}
	p.submitJob(w, r, sub)
	return true
}

// diarizationLane resolves the diarization lane of asr (the request's
// diarization_model, else asr's flexinfer.ai/diarization-model) and returns
// why a diarize job may not use it, or "" when it may: a pyannote Model that
// serves the diarization path by name and whose GPUProfile certifies it
// (condition BackendCertificate=True, set by the controller's gate).
func (p *Proxy) diarizationLane(ctx context.Context, asr *aiv1alpha2.Model, override string) (string, *aiv1alpha2.Model, string) {
	name := strings.TrimSpace(override)
	if name == "" && asr != nil {
		name = strings.TrimSpace(asr.Annotations[constants.AnnotationDiarizationModel])
	}
	if name == "" {
		return "", nil, "the ASR lane names no diarization lane (" + constants.AnnotationDiarizationModel + ") and the request sets no " + formDiarizationModel
	}
	lane := p.jobLaneModel(ctx, name)
	switch {
	case lane == nil:
		return name, nil, fmt.Sprintf("diarization lane %q does not exist", name)
	case lane.Spec.Backend != backendPyannote:
		return name, lane, fmt.Sprintf("diarization lane %q runs backend %q, not %s", name, lane.Spec.Backend, backendPyannote)
	case litellmExplicitlyDisabled(lane):
		return name, lane, fmt.Sprintf("diarization lane %q is parked and not served by name", name)
	case !modelServesPath(lane, pathAudioDiarizations):
		return name, lane, fmt.Sprintf("diarization lane %q does not serve %s", name, pathAudioDiarizations)
	}
	cond := meta.FindStatusCondition(lane.Status.Conditions, aiv1alpha2.ConditionBackendCertificate)
	if cond == nil || cond.Status != "True" {
		why := "the controller has not certified it on this GPU profile"
		if cond != nil && cond.Message != "" {
			why = cond.Message
		}
		return name, lane, fmt.Sprintf("diarization lane %q is not certified: %s", name, why)
	}
	return name, lane, ""
}

// writeDiarizationNotCertified is the 501 for a diarize request that has no
// certified diarization lane. The lane block describes the diarization lane
// when there is one.
func (p *Proxy) writeDiarizationNotCertified(w http.ResponseWriter, model string, lane *aiv1alpha2.Model, reason string) {
	p.writeLaneError(w, lane, laneError{
		status:  http.StatusNotImplemented,
		errType: validation.ErrorTypeInvalidRequest,
		code:    validation.CodeDiarizationNotCertified,
		message: fmt.Sprintf("diarize=true is not available for '%s': %s; GET /v1/models/%s shows flexinfer.diarization",
			model, reason, model),
		param:      formDiarize,
		retryAfter: -1,
	})
}

// DiarizationDescriptor is the "diarization" block of an ASR or diarization
// lane's cold capability descriptor: whether diarize=true is accepted now,
// through which lane, and the error code and reason a diarize request would
// get otherwise. Every key is always present.
type DiarizationDescriptor struct {
	Available bool    `json:"available"`
	Model     *string `json:"model"`
	Code      *string `json:"code"`
	Reason    *string `json:"reason"`
}

// diarizationDescriptor describes m's diarization: for a lane that serves
// transcriptions, the lane a diarize job on it would call; for a pyannote lane,
// its own certificate. Other lanes get none.
func (p *Proxy) diarizationDescriptor(ctx context.Context, m *aiv1alpha2.Model) *DiarizationDescriptor {
	if m == nil {
		return nil
	}
	var name, reason string
	switch {
	case m.Spec.Backend == backendPyannote:
		name, _, reason = p.diarizationLane(ctx, nil, m.Name)
	case servesTranscriptions(m):
		name, _, reason = p.diarizationLane(ctx, m, "")
	default:
		return nil
	}
	d := &DiarizationDescriptor{Available: reason == "", Model: strPtrOrNil(name)}
	if reason != "" {
		code := validation.CodeDiarizationNotCertified
		d.Code, d.Reason = &code, &reason
	}
	return d
}

// servesTranscriptions reports a lane that answers /v1/audio/transcriptions by
// declaration (serve-paths) or by backend.
func servesTranscriptions(m *aiv1alpha2.Model) bool {
	if strings.TrimSpace(m.Annotations[constants.AnnotationServePaths]) != "" {
		return modelServesPath(m, pathAudioTranscriptions)
	}
	return m.Spec.Backend == "whispercpp"
}

// ── runner ───────────────────────────────────────────────────────

func isDiarizeJob(j *aiv1alpha2.InferenceJob) bool {
	return j.Annotations[jobAnnotationPipeline] == jobPipelineDiarize
}

// diarizeRun is one diarize job's pipeline state across attempts: the
// request, then the transcription, then the turns, each kept once it
// succeeded so a retry resumes at the step that failed.
type diarizeRun struct {
	r    *jobRunner
	lane string

	raw         []byte
	contentType string
	boundary    string

	transcript []byte
	turns      *diarizationResult
}

// diarizationResult is the diarization lane's answer (build/server-pyannote.py).
type diarizationResult struct {
	Model       string            `json:"model"`
	NumSpeakers int               `json:"num_speakers"`
	Segments    []transcript.Turn `json:"segments"`
}

func newDiarizeRun(r *jobRunner, j *aiv1alpha2.InferenceJob) *diarizeRun {
	return &diarizeRun{r: r, lane: j.Annotations[jobAnnotationDiarizationModel]}
}

// unusable is a terminal attempt: retrying the same lanes cannot fix it.
func unusable(format string, args ...any) *jobAttempt {
	return &jobAttempt{status: http.StatusUnprocessableEntity, code: codeTranscriptUnusable, message: fmt.Sprintf(format, args...)}
}

// attempt runs the steps that have not succeeded yet.
func (d *diarizeRun) attempt(ctx context.Context, j *aiv1alpha2.InferenceJob, progress *atomic.Int32) (*jobAttempt, error) {
	if d.raw == nil {
		body, contentType, err := d.r.requestBody(ctx, j)
		if err != nil {
			return &jobAttempt{}, err
		}
		raw, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			return &jobAttempt{}, fmt.Errorf("reading the stored request: %w", err)
		}
		mediaType, params, _ := mime.ParseMediaType(contentType)
		if mediaType != "multipart/form-data" || params["boundary"] == "" {
			return unusable("a diarize job needs a multipart transcription request, got %q", contentType), nil
		}
		d.raw, d.contentType, d.boundary = raw, contentType, params["boundary"]
	}

	if d.transcript == nil {
		asr, err := rewriteMultipart(d.raw, d.boundary, func(field string) bool {
			switch field {
			case "background", formDiarize, formDiarizationModel, "response_format", "stream",
				formTimestampGranularity, "timestamp_granularities":
				return false
			}
			for _, f := range diarizeSpeakerFields {
				if field == f {
					return false
				}
			}
			return true
		}, [][2]string{{"response_format", "verbose_json"}, {formTimestampGranularity, "word"}, {formTimestampGranularity, "segment"}})
		if err != nil {
			return unusable("%v", err), nil
		}
		a, err := d.r.forward(ctx, j, jobForward{path: j.Spec.Path, contentType: d.contentType, body: bytes.NewReader(asr), firstByte: jobProgressLaneReady, step: "asr"}, progress)
		if err != nil || a.status/100 != 2 {
			return a, err
		}
		body, err := a.spool.contents()
		a.close()
		if err != nil {
			return unusable("reading the transcription: %v", err), nil
		}
		d.transcript = body
		raiseProgress(progress, jobProgressTranscribed)
	}

	if d.turns == nil {
		diar, err := rewriteMultipart(d.raw, d.boundary, func(field string) bool {
			if field == "file" {
				return true
			}
			for _, f := range diarizeSpeakerFields {
				if field == f {
					return true
				}
			}
			return false
		}, [][2]string{{"model", d.lane}})
		if err != nil {
			return unusable("%v", err), nil
		}
		a, err := d.r.forward(ctx, j, jobForward{path: pathAudioDiarizations, contentType: d.contentType, body: bytes.NewReader(diar), firstByte: jobProgressFirstByte, step: "diarize"}, progress)
		if a != nil {
			a.lane = d.lane
		}
		if err != nil || a.status/100 != 2 {
			return a, err
		}
		body, err := a.spool.contents()
		a.close()
		var turns diarizationResult
		if err == nil {
			err = json.Unmarshal(body, &turns)
		}
		if err != nil {
			return unusable("the diarization lane %s answered no speaker turns: %v", d.lane, err), nil
		}
		d.turns = &turns
	}

	out, stats, err := transcript.Diarize(d.transcript, transcript.DiarizationBlock{
		Model: d.lane, Pipeline: d.turns.Model, NumSpeakers: d.turns.NumSpeakers, Segments: d.turns.Segments,
	}, transcript.DefaultSpeakerGap)
	if err != nil {
		return unusable("%v", err), nil
	}
	slog.Info("diarize job merged", "job", j.Name, "asr", j.Spec.Model, "diarization", d.lane,
		"words", stats.Words, "labelled", stats.Labelled, "speakers", d.turns.NumSpeakers, "turns", len(d.turns.Segments))
	spool, err := newJobSpoolWriter(d.r.cfg.SpoolDir, d.r.cfg.MaxOutputBytes, nil)
	if err != nil {
		return &jobAttempt{}, err
	}
	spool.Header().Set("Content-Type", "application/json")
	spool.WriteHeader(http.StatusOK)
	if _, err := spool.Write(out); err != nil {
		spool.discard()
		return &jobAttempt{}, err
	}
	return &jobAttempt{status: http.StatusOK, header: spool.header, spool: spool}, nil
}

func raiseProgress(progress *atomic.Int32, to int32) {
	for {
		cur := progress.Load()
		if cur >= to || progress.CompareAndSwap(cur, to) {
			return
		}
	}
}
