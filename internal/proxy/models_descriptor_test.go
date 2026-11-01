package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func idleWanLane() *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "wan21-t2v-1p3b-gfx1100", Namespace: "default",
			Annotations: map[string]string{constants.AnnotationServePaths: "/v1/videos/generations"}},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "diffusers", Source: "pvc://a/b",
			Config:  &apiextensionsv1.JSON{Raw: []byte(`{"pipelineMode":"text2video"}`)},
			LiteLLM: &aiv1alpha2.LiteLLMSpec{Enabled: boolPtr(true), Aliases: []string{"video-gen"}},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase:   aiv1alpha2.ModelPhaseIdle,
			Metrics: &aiv1alpha2.MetricsStatus{LoadTimeSeconds: "212.0"},
		},
	}
}

// GET /v1/models/{name} answers a cold lane from its CR: limits, phase and ETA,
// and wakes nothing.
func TestGetModelDescriptorAnswersColdWithoutWaking(t *testing.T) {
	act := &countingActivator{}
	p := newGateTestProxy(t, act, idleWanLane())

	rec := httptest.NewRecorder()
	p.newServeMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/video-gen", nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got OpenAIModel
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "wan21-t2v-1p3b-gfx1100", got.ID, "the alias resolves to the CR")
	assert.Equal(t, "model", got.Object)
	require.NotNil(t, got.FlexInfer)
	d := got.FlexInfer
	assert.Equal(t, "Idle", *d.Phase)
	assert.False(t, d.Ready)
	require.NotNil(t, d.ETASeconds)
	assert.Equal(t, 212, *d.ETASeconds, "an idle lane is a whole cold start away")
	assert.Equal(t, []string{"/v1/videos/generations"}, d.ServePaths)
	assert.True(t, d.ServePathsDeclared)
	assert.Equal(t, []string{"video"}, d.Modalities.Output)
	require.NotNil(t, d.Limits)
	require.NotNil(t, d.Limits.Video)
	assert.Equal(t, 832*480, d.Limits.Video.MaxPixels)
	assert.Equal(t, 81, d.Limits.Video.MaxNumFrames)

	assert.Equal(t, 0, act.touches, "a descriptor read is not demand")
	_, queued := p.queues.Load("wan21-t2v-1p3b-gfx1100")
	assert.False(t, queued, "a descriptor read never queues")

	var raw struct {
		FlexInfer map[string]json.RawMessage `json:"flexinfer"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	for _, key := range []string{"phase", "loading_substage", "ready", "eta_seconds", "reroute_to", "serve_paths", "modalities", "limits"} {
		_, ok := raw.FlexInfer[key]
		assert.True(t, ok, "flexinfer.%s missing", key)
	}
}

func TestGetModelDescriptorParkedAndUnknown(t *testing.T) {
	p := newGateTestProxy(t, &countingActivator{}, parkedModel("parked", "Idle"))
	mux := p.newServeMux()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/parked", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, validation.CodeModelParked, envelope(t, rec).Code)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/nope", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"model_not_found"`)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/or/kimi-k3", nil))
	require.Equal(t, http.StatusNotFound, rec.Code, "a gateway alias is not a CR name")
}

func TestListModelsCarriesTheDescriptor(t *testing.T) {
	ready := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "workhorse", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b",
			Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxModelLen":131072}`)}},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
	}
	p := newGateTestProxy(t, &countingActivator{}, ready, idleWanLane())

	rec := httptest.NewRecorder()
	p.handleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp OpenAIModelsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	byID := map[string]OpenAIModel{}
	for _, m := range resp.Data {
		byID[m.ID] = m
	}
	wh := byID["workhorse"]
	require.NotNil(t, wh.FlexInfer)
	assert.True(t, wh.FlexInfer.Ready)
	assert.Equal(t, 0, *wh.FlexInfer.ETASeconds)
	assert.Equal(t, 131072, wh.FlexInfer.MaxModelLen)
	assert.Nil(t, wh.FlexInfer.Limits)
	assert.Equal(t, []string{"text"}, wh.FlexInfer.Modalities.Output)
	require.NotNil(t, byID["wan21-t2v-1p3b-gfx1100"].FlexInfer)
	assert.NotNil(t, byID["wan21-t2v-1p3b-gfx1100"].FlexInfer.Limits.Video)
}
