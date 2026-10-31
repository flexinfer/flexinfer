package modelmeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func capModel(backend, config string, annotations map[string]string) *aiv1alpha2.Model {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "lane", Annotations: annotations},
		Spec:       aiv1alpha2.ModelSpec{Backend: backend, Source: "pvc://a/b"},
	}
	if config != "" {
		m.Spec.Config = &apiextensionsv1.JSON{Raw: []byte(config)}
	}
	return m
}

func TestResolveCapabilitiesWanT2V(t *testing.T) {
	c := ResolveCapabilities(capModel("diffusers",
		`{"pipelineMode":"text2video","videoSize":"640x384","videoFrames":49}`,
		map[string]string{constants.AnnotationServePaths: "/v1/videos/generations"}))

	if !c.ServePathsDeclared || len(c.ServePaths) != 1 || c.ServePaths[0] != "/v1/videos/generations" {
		t.Fatalf("serve paths = %v declared=%v", c.ServePaths, c.ServePathsDeclared)
	}
	if got := strings.Join(c.Modalities.Input, ","); got != "text" {
		t.Errorf("input = %q", got)
	}
	if got := strings.Join(c.Modalities.Output, ","); got != "video" {
		t.Errorf("output = %q", got)
	}
	if c.MaxModelLen != 0 {
		t.Errorf("a media lane has no context window, got %d", c.MaxModelLen)
	}
	if c.Limits == nil || c.Limits.Video == nil || c.Limits.Image != nil {
		t.Fatalf("limits = %+v", c.Limits)
	}
	v := c.Limits.Video
	if v.MaxPixels != 832*480 || v.MaxNumFrames != 81 || v.NumFramesStep != 4 || v.MaxFPS != 24 || v.DimensionMultiple != 16 {
		t.Errorf("server bounds = %+v", v)
	}
	if v.DefaultSize != "640x384" || v.DefaultNumFrames != 49 || v.DefaultFPS != 16 {
		t.Errorf("config defaults not applied: %+v", v)
	}
	if v.RequiresImage {
		t.Error("text2video does not need an image")
	}
}

func TestResolveCapabilitiesWanImage2Video(t *testing.T) {
	c := ResolveCapabilities(capModel("diffusers", `{"pipelineMode":"image2video"}`, nil))

	if c.ServePathsDeclared || c.ServePaths[0] != "/v1/videos/generations" {
		t.Fatalf("serve paths = %v", c.ServePaths)
	}
	if got := strings.Join(c.Modalities.Input, ","); got != "text,image" {
		t.Errorf("input = %q", got)
	}
	v := c.Limits.Video
	if !v.RequiresImage || v.MaxImageBase64Chars != 16*1024*1024 || v.MaxImagePixels != 16_777_216 {
		t.Errorf("image2video image limits = %+v", v)
	}
}

func TestResolveCapabilitiesQwenImage(t *testing.T) {
	c := ResolveCapabilities(capModel("diffusers", `{"numInferenceSteps":28}`,
		map[string]string{constants.AnnotationServePaths: "/v1/images/generations, /v1/images/edits"}))

	if got := strings.Join(c.Modalities.Input, ","); got != "text,image" {
		t.Errorf("input = %q", got)
	}
	if c.Limits == nil || c.Limits.Image == nil {
		t.Fatalf("limits = %+v", c.Limits)
	}
	if img := c.Limits.Image; img.MaxN != 10 || img.MaxNumInferenceSteps != 100 || img.DefaultNumInferenceSteps != 28 {
		t.Errorf("image limits = %+v", img)
	}
}

func TestResolveCapabilitiesChatLane(t *testing.T) {
	c := ResolveCapabilities(capModel("vllm", `{"maxModelLen":131072}`, nil))

	if c.ServePathsDeclared || strings.Join(c.ServePaths, ",") != "/v1/chat/completions,/v1/completions" {
		t.Errorf("serve paths = %v", c.ServePaths)
	}
	if c.MaxModelLen != 131072 {
		t.Errorf("max_model_len = %d", c.MaxModelLen)
	}
	if c.Limits != nil {
		t.Errorf("token lanes carry no media limits: %+v", c.Limits)
	}
}

func TestResolveCapabilitiesEmbeddingAndASR(t *testing.T) {
	emb := ResolveCapabilities(capModel("llamacpp", `{"contextSize":8192}`,
		map[string]string{constants.AnnotationServePaths: "/v1/embeddings"}))
	if strings.Join(emb.Modalities.Output, ",") != "embedding" || emb.MaxModelLen != 8192 {
		t.Errorf("embedding lane = %+v", emb)
	}
	asr := ResolveCapabilities(capModel("whispercpp", "", nil))
	if strings.Join(asr.ServePaths, ",") != "/v1/audio/transcriptions" ||
		strings.Join(asr.Modalities.Input, ",") != "audio" || strings.Join(asr.Modalities.Output, ",") != "text" {
		t.Errorf("asr lane = %+v", asr)
	}
}

// TestDiffusersLimitsMatchTheServer is the drift guard for the mirrored
// bounds: each one must still appear, verbatim, in build/server-diffusers.py.
// If the server changes a bound, change it in capabilities.go too.
func TestDiffusersLimitsMatchTheServer(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "build", "server-diffusers.py"))
	if err != nil {
		t.Fatalf("read server: %v", err)
	}
	for _, want := range []string{
		`n: int = Field(default=1, ge=1, le=10)`,
		`num_inference_steps: Optional[int] = Field(default=None, ge=1, le=100)`,
		`guidance_scale: Optional[float] = Field(default=None, ge=0.0, le=20.0)`,
		`prompt: str = Field(min_length=1, max_length=4000)`,
		`num_frames: Optional[int] = Field(default=None, ge=1, le=81)`,
		`fps: Optional[int] = Field(default=None, ge=1, le=24)`,
		`num_inference_steps: Optional[int] = Field(default=None, ge=1, le=50)`,
		`image: Optional[str] = Field(default=None, max_length=16 * 1024 * 1024)`,
		`if width * height > 832 * 480:`,
		`width % 16 != 0 or height % 16 != 0`,
		`(value - 1) % 4 != 0`,
		`if image.width * image.height > 16_777_216:`,
		`return _env_int("DEFAULT_VIDEO_NUM_FRAMES") or 81`,
		`return _env_int("DEFAULT_VIDEO_FPS") or 16`,
		`os.environ.get("DEFAULT_VIDEO_SIZE") or "832x480"`,
		`seconds: float = Field(default=20.0, gt=0.0, le=47.0)`,
		`num_inference_steps: Optional[int] = Field(default=None, ge=10, le=200)`,
		`prompt: str = Field(min_length=1, max_length=2000)`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("server-diffusers.py no longer contains %q: update the mirrored limits in capabilities.go", want)
		}
	}
}
