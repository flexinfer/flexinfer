package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/transcript"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The ASR lane's verbose_json (words as the whispercpp runtime ingress adds
// them) and the diarization lane's turns (build/server-pyannote.py).
const (
	diarASRReply = `{"task":"transcribe","language":"english","duration":4.0,"text":" Hi all. Thanks, it works.",` +
		`"segments":[{"id":0,"start":0,"end":4,"text":" Hi all. Thanks, it works."}],` +
		`"words":[{"word":"Hi","start":0.1,"end":0.3,"probability":0.9},{"word":"all.","start":0.3,"end":0.7,"probability":0.8},` +
		`{"word":"Thanks,","start":2.0,"end":2.4,"probability":0.9},{"word":"it","start":2.4,"end":2.6,"probability":0.9},` +
		`{"word":"works.","start":2.6,"end":3.1,"probability":0.9}]}`
	diarTurnsReply = `{"object":"audio.diarization","model":"pyannote/speaker-diarization-3.1","duration":4.0,"num_speakers":2,` +
		`"speakers":["SPEAKER_00","SPEAKER_01"],"segments":[{"start":0.0,"end":1.0,"speaker":"SPEAKER_00"},{"start":1.8,"end":3.5,"speaker":"SPEAKER_01"}]}`
	diarAudio = "RIFF\x00\x01meeting-audio"
)

func diarizationLaneModel(name string, certified bool, message string) *aiv1alpha2.Model {
	lane := readyLane(name, backendPyannote, map[string]string{constants.AnnotationServePaths: pathAudioDiarizations})
	status, reason := metav1.ConditionFalse, aiv1alpha2.ReasonBackendNotCertified
	if certified {
		status, reason = metav1.ConditionTrue, aiv1alpha2.ReasonBackendCertified
	}
	lane.Status.Conditions = []metav1.Condition{{
		Type: aiv1alpha2.ConditionBackendCertificate, Status: status, Reason: reason, Message: message,
		LastTransitionTime: metav1.Now(),
	}}
	return lane
}

func asrLaneModel(diarizationModel string) *aiv1alpha2.Model {
	annotations := map[string]string{constants.AnnotationServePaths: pathAudioTranscriptions}
	if diarizationModel != "" {
		annotations[constants.AnnotationDiarizationModel] = diarizationModel
	}
	return readyLane("whisper", "whispercpp", annotations)
}

func newDiarizeFixture(t *testing.T, models ...*aiv1alpha2.Model) *jobFixture {
	t.Helper()
	f := newJobFixture(t, models...)
	f.replies.Store("whisper", diarASRReply)
	f.replies.Store("pyannote-vii", diarTurnsReply)
	return f
}

func (f *jobFixture) transcribe(t *testing.T, fields map[string]string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	body, ct := multipartBody(t, fields, "meeting.wav", []byte(diarAudio))
	r := httptest.NewRequest(http.MethodPost, pathAudioTranscriptions, bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	f.p.handler().ServeHTTP(rec, r)
	return rec, body
}

func (f *jobFixture) jobCount(t *testing.T) int {
	t.Helper()
	var list aiv1alpha2.InferenceJobList
	require.NoError(t, f.client.List(context.Background(), &list))
	return len(list.Items)
}

func assertNoUpstreamRequest(t *testing.T, f *jobFixture) {
	t.Helper()
	select {
	case <-f.bodies:
		t.Fatal("no lane may be called")
	default:
	}
}

func TestDiarizeJobTranscribesDiarizesAndLabelsWords(t *testing.T) {
	f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, "certified"))

	rec, _ := f.transcribe(t, map[string]string{
		"model": "whisper", "diarize": "true", "response_format": "json", "language": "en", "num_speakers": "2",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var obj jobObject
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &obj))
	assert.Equal(t, "queued", obj.Status, "diarize=true is a job without background=true")
	assert.Equal(t, "whisper", obj.Model)
	assert.Equal(t, pathAudioTranscriptions, obj.Path)
	assertNoUpstreamRequest(t, f)

	j := f.waitJob(t, obj.ID)
	require.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase, j.Status.Error)
	assert.Equal(t, jobPipelineDiarize, j.Annotations[jobAnnotationPipeline])
	assert.Equal(t, "pyannote-vii", j.Annotations[jobAnnotationDiarizationModel])
	assert.Equal(t, "whisper", j.Status.Lane)

	ct := "multipart/form-data; boundary=" + boundaryOf(t, j)
	asr := parseMultipart(t, <-f.bodies, ct)
	assert.Equal(t, diarAudio, asr["file"], "the audio reaches the ASR lane byte for byte")
	assert.Equal(t, "verbose_json", asr["response_format"], "the job asks the ASR lane for words")
	assert.Equal(t, "en", asr["language"])
	for _, gone := range []string{"diarize", "num_speakers", formDiarizationModel, "background"} {
		assert.NotContains(t, asr, gone)
	}
	diar := parseMultipart(t, <-f.bodies, ct)
	assert.Equal(t, diarAudio, diar["file"])
	assert.Equal(t, "pyannote-vii", diar["model"])
	assert.Equal(t, "2", diar["num_speakers"])
	assert.NotContains(t, diar, "language")
	assert.NotContains(t, diar, "response_format")

	rec, got := f.do(t, http.MethodGet, "/v1/jobs/"+obj.ID, nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got.Output)
	assert.Equal(t, "application/json", got.Output.ContentType)
	rec, _ = f.do(t, http.MethodGet, got.Output.URL, nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var doc transcript.Diarized
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	speakers := make([]string, len(doc.Words))
	for i, w := range doc.Words {
		speakers[i] = w.Speaker
	}
	assert.Equal(t, []string{"SPEAKER_00", "SPEAKER_00", "SPEAKER_01", "SPEAKER_01", "SPEAKER_01"}, speakers)
	assert.Equal(t, " Hi all. Thanks, it works.", doc.Text)
	assert.Equal(t, []transcript.Utterance{
		{Speaker: "SPEAKER_00", Start: 0.1, End: 0.7, Text: "Hi all."},
		{Speaker: "SPEAKER_01", Start: 2.0, End: 3.1, Text: "Thanks, it works."},
	}, doc.Utterances)
	assert.Equal(t, "pyannote-vii", doc.Diarization.Model)
	assert.Equal(t, "pyannote/speaker-diarization-3.1", doc.Diarization.Pipeline)
	assert.Equal(t, 2, doc.Diarization.NumSpeakers)
	assert.Len(t, doc.Diarization.Segments, 2)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.Contains(t, raw, "language", "the ASR lane's fields are kept")
	assert.Contains(t, raw, "segments")
}

// boundaryOf reads the multipart boundary of j's stored request.
func boundaryOf(t *testing.T, j *aiv1alpha2.InferenceJob) string {
	t.Helper()
	require.Len(t, j.Spec.InputRefs, 1)
	ct := j.Spec.InputRefs[0].ContentType
	i := strings.Index(ct, "boundary=")
	require.GreaterOrEqual(t, i, 0, ct)
	return ct[i+len("boundary="):]
}

func TestSpeakerTimestampGranularityIsADiarizeJob(t *testing.T) {
	f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, ""))
	rec, _ := f.transcribe(t, map[string]string{"model": "whisper", "timestamp_granularities[]": "speaker"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var obj jobObject
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &obj))
	j := f.waitJob(t, obj.ID)
	require.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase, j.Status.Error)
	ct := "multipart/form-data; boundary=" + boundaryOf(t, j)
	asr := parseMultipart(t, <-f.bodies, ct)
	assert.Equal(t, "segment", asr["timestamp_granularities[]"], "speaker is replaced by word and segment for the ASR lane")
}

// recordingActivator records which lanes a job's waits demand.
type recordingActivator struct {
	countingActivator
	mu    sync.Mutex
	lanes []string
}

func (a *recordingActivator) TouchLastActiveTime(_ context.Context, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lanes = append(a.lanes, name)
}

func TestDiarizeJobRetriesOnlyTheStepThatFailed(t *testing.T) {
	f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, ""))
	activator := &recordingActivator{}
	f.p.activator = activator
	f.statusFor.Store("pyannote-vii", http.StatusServiceUnavailable)
	jobRetryMinWait = 10 * time.Millisecond
	t.Cleanup(func() { jobRetryMinWait = 5 * time.Second })

	rec, _ := f.transcribe(t, map[string]string{"model": "whisper", "diarize": "true"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var obj jobObject
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &obj))
	f.p.jobs.pass(context.Background())
	<-f.bodies // transcription
	<-f.bodies // diarization, 503
	f.statusFor.Delete("pyannote-vii")

	j := f.waitJob(t, obj.ID)
	require.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase, j.Status.Error)
	assert.EqualValues(t, 1, f.requestCount("whisper"), "a retry resumes after the transcription it already has")
	assert.GreaterOrEqual(t, f.requestCount("pyannote-vii"), int32(2))
	activator.mu.Lock()
	defer activator.mu.Unlock()
	assert.Contains(t, activator.lanes, "pyannote-vii", "the wait keeps the diarization lane demanded")
}

func TestDiarizeJobFailsWhenTheTranscriptHasNoWords(t *testing.T) {
	f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, ""))
	f.replies.Store("whisper", `{"text":"hello"}`)
	rec, _ := f.transcribe(t, map[string]string{"model": "whisper", "diarize": "true"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var obj jobObject
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &obj))
	j := f.waitJob(t, obj.ID)
	assert.Equal(t, aiv1alpha2.InferenceJobFailed, j.Status.Phase)
	require.NotNil(t, j.Status.Error)
	assert.Equal(t, codeTranscriptUnusable, j.Status.Error.Code)
	assert.Contains(t, j.Status.Error.Message, "verbose_json")
}

func TestDiarizeWithoutACertifiedLaneIsAnswered501(t *testing.T) {
	cases := []struct {
		name   string
		models []*aiv1alpha2.Model
		reason string
	}{
		{"profile not certified", []*aiv1alpha2.Model{asrLaneModel("pyannote-vii"),
			diarizationLaneModel("pyannote-vii", false, "GPUProfile gfx906 does not list pyannote in spec.runtime.bundledBackends")},
			"does not list pyannote"},
		{"no condition yet", []*aiv1alpha2.Model{asrLaneModel("pyannote-vii"), readyLane("pyannote-vii", backendPyannote, nil)},
			"has not certified it"},
		{"no diarization lane named", []*aiv1alpha2.Model{asrLaneModel("")}, constants.AnnotationDiarizationModel},
		{"named lane missing", []*aiv1alpha2.Model{asrLaneModel("pyannote-vii")}, "does not exist"},
		{"named lane is not pyannote", []*aiv1alpha2.Model{asrLaneModel("pyannote-vii"), readyLane("pyannote-vii", "llamacpp", nil)},
			`runs backend "llamacpp"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDiarizeFixture(t, tc.models...)
			rec, _ := f.transcribe(t, map[string]string{"model": "whisper", "diarize": "true"})
			require.Equal(t, http.StatusNotImplemented, rec.Code, rec.Body.String())
			var e struct {
				Error struct {
					Type    string `json:"type"`
					Code    string `json:"code"`
					Param   string `json:"param"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
			assert.Equal(t, validation.CodeDiarizationNotCertified, e.Error.Code)
			assert.Equal(t, validation.ErrorTypeInvalidRequest, e.Error.Type)
			assert.Equal(t, "diarize", e.Error.Param)
			assert.Contains(t, e.Error.Message, tc.reason)
			assert.Empty(t, rec.Header().Get("Retry-After"), "retrying cannot help")
			assert.Equal(t, 0, f.jobCount(t), "no job is created")
			assertNoUpstreamRequest(t, f)

			// The cold descriptor says the same without a request.
			rec, _ = f.do(t, http.MethodGet, "/v1/models/whisper", nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var model OpenAIModel
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &model))
			require.NotNil(t, model.FlexInfer)
			d := model.FlexInfer.Diarization
			require.NotNil(t, d)
			assert.False(t, d.Available)
			require.NotNil(t, d.Code)
			assert.Equal(t, validation.CodeDiarizationNotCertified, *d.Code)
			require.NotNil(t, d.Reason)
			assert.Contains(t, *d.Reason, tc.reason)
		})
	}
}

func TestDiarizationDescriptor(t *testing.T) {
	f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, ""),
		readyLane("chat", "vllm", nil))
	describe := func(name string) *DiarizationDescriptor {
		rec, _ := f.do(t, http.MethodGet, "/v1/models/"+name, nil, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var model OpenAIModel
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &model))
		require.NotNil(t, model.FlexInfer)
		return model.FlexInfer.Diarization
	}
	for _, name := range []string{"whisper", "pyannote-vii"} {
		d := describe(name)
		require.NotNil(t, d, name)
		assert.True(t, d.Available, name)
		require.NotNil(t, d.Model, name)
		assert.Equal(t, "pyannote-vii", *d.Model, name)
		assert.Nil(t, d.Code, name)
		assert.Nil(t, d.Reason, name)
	}
	assert.Nil(t, describe("chat"), "a lane that serves no transcriptions has no diarization block")

	rec, _ := f.do(t, http.MethodGet, "/v1/models/whisper", nil, nil)
	assert.Contains(t, rec.Body.String(), `"diarization":{"available":true,"model":"pyannote-vii","code":null,"reason":null}`)
}

func TestDiarizeRequestValidation(t *testing.T) {
	f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, ""))
	for name, tc := range map[string]struct {
		fields map[string]string
		status int
		param  string
	}{
		"subtitle format":   {map[string]string{"model": "whisper", "diarize": "true", "response_format": "srt"}, http.StatusBadRequest, "response_format"},
		"streaming":         {map[string]string{"model": "whisper", "diarize": "true", "stream": "true"}, http.StatusBadRequest, "stream"},
		"no model":          {map[string]string{"diarize": "true"}, http.StatusBadRequest, ""},
		"unknown asr model": {map[string]string{"model": "nope", "diarize": "true"}, http.StatusNotFound, ""},
	} {
		t.Run(name, func(t *testing.T) {
			rec, _ := f.transcribe(t, tc.fields)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			if tc.param != "" {
				assert.Contains(t, rec.Body.String(), `"param":"`+tc.param+`"`)
			}
			assert.Equal(t, 0, f.jobCount(t))
		})
	}

	f.p.jobs = nil
	rec, _ := f.transcribe(t, map[string]string{"model": "whisper", "diarize": "true"})
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
	assert.Contains(t, rec.Body.String(), codeJobsDisabled, "diarize needs the job API")
}

// TestTranscriptionWithoutDiarizeIsUnchanged: a transcription that does not
// ask for speakers, with jobs on or off, reaches the ASR lane synchronously
// with the exact bytes the client sent and returns the lane's answer.
func TestTranscriptionWithoutDiarizeIsUnchanged(t *testing.T) {
	for _, jobsOn := range []bool{true, false} {
		for name, fields := range map[string]map[string]string{
			"plain":            {"model": "whisper", "response_format": "verbose_json", "timestamp_granularities[]": "word"},
			"diarize=false":    {"model": "whisper", "diarize": "false"},
			"unknown diarizer": {"model": "whisper", "diarization_model": "pyannote-vii"},
		} {
			t.Run(name, func(t *testing.T) {
				f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, ""))
				if !jobsOn {
					f.p.jobs = nil
				}
				rec, sent := f.transcribe(t, fields)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				assert.JSONEq(t, diarASRReply, rec.Body.String())
				assert.Equal(t, sent, <-f.bodies, "the lane gets the client's bytes")
				assert.Zero(t, f.requestCount("pyannote-vii"))
				assert.Equal(t, 0, f.jobCount(t))
			})
		}
	}

	t.Run("a JSON body is never a diarize job", func(t *testing.T) {
		f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, ""))
		body := `{"model":"whisper","diarize":true}`
		rec, _ := f.do(t, http.MethodPost, pathAudioTranscriptions, strings.NewReader(body), nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		// The JSON path's existing model-name rewrite applies; nothing else.
		var forwarded map[string]any
		require.NoError(t, json.Unmarshal(<-f.bodies, &forwarded))
		assert.Equal(t, true, forwarded["diarize"])
		assert.Zero(t, f.requestCount("pyannote-vii"))
		assert.Equal(t, 0, f.jobCount(t))
	})

	t.Run("background without diarize stores the upload as before", func(t *testing.T) {
		f := newDiarizeFixture(t, asrLaneModel("pyannote-vii"), diarizationLaneModel("pyannote-vii", true, ""))
		rec, _ := f.transcribe(t, map[string]string{"model": "whisper", "background": "true"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var obj jobObject
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &obj))
		j := f.waitJob(t, obj.ID)
		require.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase, j.Status.Error)
		assert.Empty(t, j.Annotations[jobAnnotationPipeline], "a plain background job runs one forward")
		assert.Zero(t, f.requestCount("pyannote-vii"))
		rec, _ = f.do(t, http.MethodGet, "/v1/jobs/"+obj.ID+"/content", nil, nil)
		assert.JSONEq(t, diarASRReply, rec.Body.String(), "the lane's answer is stored unchanged")
	})
}

func TestRewriteMultipartKeepsPartsByteForByte(t *testing.T) {
	body, ct := multipartBody(t, map[string]string{"model": "whisper", "diarize": "true"}, "a.wav", []byte("\x00\xffRIFF"))
	boundary := ct[strings.Index(ct, "boundary=")+len("boundary="):]
	out, err := rewriteMultipart(body, boundary, func(name string) bool { return name != "diarize" }, [][2]string{{"response_format", "verbose_json"}})
	require.NoError(t, err)
	fields := parseMultipart(t, out, ct)
	assert.Equal(t, map[string]string{"model": "whisper", "file": "\x00\xffRIFF", "response_format": "verbose_json"}, fields)

	same, err := rewriteMultipart(body, boundary, func(string) bool { return true }, nil)
	require.NoError(t, err)
	assert.Equal(t, parseMultipart(t, body, ct), parseMultipart(t, same, ct))
}
