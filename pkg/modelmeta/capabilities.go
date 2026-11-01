package modelmeta

import (
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

// Capabilities is what a Model's lane serves and accepts, derived from its
// spec alone so a caller can read it without waking the lane: the paths it
// answers, its modalities, its context window and the request limits its
// server enforces.
//
// The diffusers limits are fixed per pipeline mode in build/server-diffusers.py
// (pydantic field bounds and the 480p video check), so they are derived from
// spec.config.pipelineMode plus the lane's configured defaults rather than
// declared per Model; TestDiffusersLimitsMatchTheServer keeps the two in step.
type Capabilities struct {
	// ServePaths are the request paths the lane answers: the
	// flexinfer.ai/serve-paths annotation when set (the proxy rejects every
	// other path), else the paths its backend serves by default.
	ServePaths []string `json:"serve_paths"`
	// ServePathsDeclared is true when ServePaths came from the annotation.
	ServePathsDeclared bool `json:"serve_paths_declared"`
	// Modalities are the input and output kinds across ServePaths.
	Modalities Modalities `json:"modalities"`
	// MaxModelLen is the context window in tokens, 0 when not a token lane or
	// not configured.
	MaxModelLen int `json:"max_model_len,omitempty"`
	// Limits are the request bounds of a media lane; nil for token lanes.
	Limits *Limits `json:"limits,omitempty"`
}

// Modalities lists input and output kinds: text, image, video, audio,
// embedding, score.
type Modalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// Limits are a media lane's request bounds, one block per output kind.
type Limits struct {
	Image *ImageLimits `json:"image,omitempty"`
	Video *VideoLimits `json:"video,omitempty"`
	Audio *AudioLimits `json:"audio,omitempty"`
}

// ImageLimits bound /v1/images/* requests on a diffusers lane.
type ImageLimits struct {
	MaxN                     int     `json:"max_n"`
	MaxNumInferenceSteps     int     `json:"max_num_inference_steps"`
	MaxGuidanceScale         float64 `json:"max_guidance_scale"`
	DefaultNumInferenceSteps int     `json:"default_num_inference_steps,omitempty"`
}

// VideoLimits bound /v1/videos/generations on a Wan lane. num_frames must be
// NumFramesStep*k + 1 and at most MaxNumFrames; width and height must be
// multiples of DimensionMultiple with width*height at most MaxPixels.
type VideoLimits struct {
	MaxPixels            int    `json:"max_pixels"`
	DimensionMultiple    int    `json:"dimension_multiple"`
	MaxNumFrames         int    `json:"max_num_frames"`
	NumFramesStep        int    `json:"num_frames_step"`
	MaxFPS               int    `json:"max_fps"`
	MaxNumInferenceSteps int    `json:"max_num_inference_steps"`
	MaxPromptChars       int    `json:"max_prompt_chars"`
	DefaultSize          string `json:"default_size"`
	DefaultNumFrames     int    `json:"default_num_frames"`
	DefaultFPS           int    `json:"default_fps"`
	// RequiresImage is set for image2video lanes (Wan VACE), which condition
	// on a first-frame image of at most MaxImageBase64Chars encoded and
	// MaxImagePixels decoded.
	RequiresImage       bool `json:"requires_image"`
	MaxImageBase64Chars int  `json:"max_image_base64_chars,omitempty"`
	MaxImagePixels      int  `json:"max_image_pixels,omitempty"`
}

// AudioLimits bound /v1/audio/generations on a Stable Audio lane.
type AudioLimits struct {
	MaxSeconds           float64 `json:"max_seconds"`
	MinNumInferenceSteps int     `json:"min_num_inference_steps"`
	MaxNumInferenceSteps int     `json:"max_num_inference_steps"`
	MaxPromptChars       int     `json:"max_prompt_chars"`
}

// Bounds enforced by build/server-diffusers.py.
var (
	diffusersImageLimits = ImageLimits{MaxN: 10, MaxNumInferenceSteps: 100, MaxGuidanceScale: 20}
	diffusersVideoLimits = VideoLimits{
		MaxPixels: 832 * 480, DimensionMultiple: 16, MaxNumFrames: 81, NumFramesStep: 4,
		MaxFPS: 24, MaxNumInferenceSteps: 50, MaxPromptChars: 4000,
		DefaultSize: "832x480", DefaultNumFrames: 81, DefaultFPS: 16,
	}
	diffusersImage2VideoImage = struct{ base64Chars, pixels int }{16 * 1024 * 1024, 16_777_216}
	diffusersAudioLimits      = AudioLimits{MaxSeconds: 47, MinNumInferenceSteps: 10, MaxNumInferenceSteps: 200, MaxPromptChars: 2000}
)

// Default paths by backend when a Model declares none.
var (
	chatPaths = []string{"/v1/chat/completions", "/v1/completions"}

	diffusersPathsByMode = map[string][]string{
		"":            {"/v1/images/generations"},
		"text2image":  {"/v1/images/generations"},
		"inpainting":  {"/v1/images/edits"},
		"instruct":    {"/v1/images/edits"},
		"img2img":     {"/v1/images/edits"},
		"controlnet":  {"/v1/images/generations"},
		"text2video":  {"/v1/videos/generations"},
		"image2video": {"/v1/videos/generations"},
		"text2audio":  {"/v1/audio/generations"},
	}
)

// pathModalities maps a request path prefix to the kinds it consumes and
// produces.
var pathModalities = []struct {
	prefix  string
	in, out []string
}{
	{"/v1/chat/completions", []string{"text"}, []string{"text"}},
	{"/v1/completions", []string{"text"}, []string{"text"}},
	{"/v1/embeddings", []string{"text"}, []string{"embedding"}},
	{"/v1/rerank", []string{"text"}, []string{"score"}},
	{"/v1/score", []string{"text"}, []string{"score"}},
	{"/classify", []string{"text"}, []string{"score"}},
	{"/v1/images/generations", []string{"text"}, []string{"image"}},
	{"/v1/images/edits", []string{"text", "image"}, []string{"image"}},
	{"/v1/images/variations", []string{"image"}, []string{"image"}},
	{"/v1/videos/generations", []string{"text"}, []string{"video"}},
	{"/v1/audio/transcriptions", []string{"audio"}, []string{"text"}},
	{"/v1/audio/translations", []string{"audio"}, []string{"text"}},
	{"/v1/audio/speech", []string{"text"}, []string{"audio"}},
	{"/v1/audio/generations", []string{"text"}, []string{"audio"}},
}

// ResolveCapabilities derives m's capabilities from its spec and annotations.
func ResolveCapabilities(m *aiv1alpha2.Model) Capabilities {
	if m == nil {
		return Capabilities{}
	}
	var c Capabilities
	if raw := strings.TrimSpace(m.Annotations[constants.AnnotationServePaths]); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.ServePaths = append(c.ServePaths, p)
			}
		}
		c.ServePathsDeclared = len(c.ServePaths) > 0
	}
	mode := strings.TrimSpace(m.Spec.ConfigString("pipelineMode", ""))
	if !c.ServePathsDeclared {
		c.ServePaths = defaultServePaths(m.Spec.Backend, mode)
	}
	c.Modalities = modalitiesFor(c.ServePaths)
	if mode == "image2video" {
		c.Modalities.Input = appendUnique(c.Modalities.Input, "image")
	}
	if !isMediaBackend(m.Spec.Backend) {
		c.MaxModelLen = ResolveTokenLimits(&m.Spec).ContextWindow
	}
	if m.Spec.Backend == backendDiffusers {
		c.Limits = diffusersLimits(&m.Spec, mode, c.Modalities.Output)
	}
	return c
}

const (
	backendDiffusers  = "diffusers"
	backendWhisperCpp = "whispercpp"
	backendSNAC       = "snac"
	backendComfyUI    = "comfyui"
)

func isMediaBackend(name string) bool {
	switch name {
	case backendDiffusers, backendWhisperCpp, backendSNAC, backendComfyUI:
		return true
	}
	return false
}

func defaultServePaths(backend, mode string) []string {
	switch backend {
	case backendDiffusers:
		if paths, ok := diffusersPathsByMode[mode]; ok {
			return paths
		}
		return diffusersPathsByMode[""]
	case backendWhisperCpp:
		return []string{"/v1/audio/transcriptions"}
	case backendSNAC:
		return []string{"/v1/audio/decode"}
	case backendComfyUI:
		return nil
	}
	return chatPaths
}

func modalitiesFor(paths []string) Modalities {
	m := Modalities{Input: []string{}, Output: []string{}}
	for _, p := range paths {
		for _, pm := range pathModalities {
			if strings.HasPrefix(p, pm.prefix) {
				for _, in := range pm.in {
					m.Input = appendUnique(m.Input, in)
				}
				for _, out := range pm.out {
					m.Output = appendUnique(m.Output, out)
				}
				break
			}
		}
	}
	return m
}

func appendUnique(list []string, v string) []string {
	for _, have := range list {
		if have == v {
			return list
		}
	}
	return append(list, v)
}

// diffusersLimits returns the server's bounds for the kinds the lane outputs,
// with the defaults its spec.config sets (numInferenceSteps, videoSize,
// videoFrames, videoFps).
func diffusersLimits(spec *aiv1alpha2.ModelSpec, mode string, outputs []string) *Limits {
	l := &Limits{}
	for _, out := range outputs {
		switch out {
		case "image":
			img := diffusersImageLimits
			img.DefaultNumInferenceSteps = spec.ConfigInt("numInferenceSteps", 0)
			l.Image = &img
		case "video":
			v := diffusersVideoLimits
			if size := strings.ToLower(strings.TrimSpace(spec.ConfigString("videoSize", ""))); size != "" {
				v.DefaultSize = size
			}
			v.DefaultNumFrames = spec.ConfigInt("videoFrames", v.DefaultNumFrames)
			v.DefaultFPS = spec.ConfigInt("videoFps", v.DefaultFPS)
			if mode == "image2video" {
				v.RequiresImage = true
				v.MaxImageBase64Chars = diffusersImage2VideoImage.base64Chars
				v.MaxImagePixels = diffusersImage2VideoImage.pixels
			}
			l.Video = &v
		case "audio":
			a := diffusersAudioLimits
			l.Audio = &a
		}
	}
	if l.Image == nil && l.Video == nil && l.Audio == nil {
		return nil
	}
	return l
}
