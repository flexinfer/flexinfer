package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/flexinfer/flexinfer/pkg/validation"
)

// AUTOMATIC1111-style image API (/sdapi/v1/*) over an image lane.
//
// Roleplay front ends (Talemate, SillyTavern) create images through the
// Stable Diffusion WebUI REST API, not the OpenAI Images API. This adapter
// answers the handful of calls they make and turns txt2img into the lane's
// own POST /v1/images/generations, dispatched through handleRequest so alias
// resolution, activation, the GPU swap and the lane's first-byte budget all
// apply as they do for any other caller.
//
// What is translated: prompt, negative_prompt, width and height, seed, and the
// batch count. What is not: steps, cfg_scale, sampler_name and scheduler.
// Those clients send Stable Diffusion defaults on every request (Talemate:
// cfg_scale 7, "DPM++ 2M"), which are wrong for a Qwen-Image lane; the lane's
// own tuned defaults apply instead.
//
// The model is override_settings.sd_model_checkpoint when the client names
// one, otherwise the configured default (FLEXINFER_A1111_MODEL). With no
// default configured every route answers 503.

const (
	pathImagesGenerations = "/v1/images/generations"

	a1111DefaultSide = 1024
	a1111MinSide     = 256
	a1111MaxSide     = 2048
	// Diffusion VAEs need both sides divisible by 16.
	a1111SideMultiple = 16
	// One Qwen-Image render takes one to three minutes; a client batch
	// setting should not queue a quarter of an hour of GPU time.
	a1111MaxImages = 4
	// A txt2img body is a prompt and a few numbers.
	a1111MaxRequestBytes = 1 << 20
)

// a1111Txt2ImgRequest is the subset of the WebUI txt2img payload the adapter
// reads. Unknown fields are ignored, as the WebUI itself does.
type a1111Txt2ImgRequest struct {
	Prompt           string         `json:"prompt"`
	NegativePrompt   string         `json:"negative_prompt"`
	Width            int            `json:"width"`
	Height           int            `json:"height"`
	Seed             *int64         `json:"seed"`
	BatchSize        int            `json:"batch_size"`
	NIter            int            `json:"n_iter"`
	OverrideSettings map[string]any `json:"override_settings"`
}

// laneImageRequest is the body the image lanes accept on
// /v1/images/generations (build/server-diffusers.py ImageGenerationRequest).
type laneImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	Size           string `json:"size"`
	N              int    `json:"n"`
	Seed           *int64 `json:"seed,omitempty"`
	ResponseFormat string `json:"response_format"`
}

type laneImageResponse struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
}

// a1111Txt2ImgResponse is the WebUI response shape: base64 images, the
// parameters that produced them, and `info` as a JSON string.
type a1111Txt2ImgResponse struct {
	Images     []string       `json:"images"`
	Parameters map[string]any `json:"parameters"`
	Info       string         `json:"info"`
}

// a1111Side clamps one image side to what a lane can render: the default when
// unset, within [256, 2048], rounded down to a multiple of 16.
func a1111Side(v int) int {
	if v <= 0 {
		v = a1111DefaultSide
	}
	v = min(max(v, a1111MinSide), a1111MaxSide)
	return v / a1111SideMultiple * a1111SideMultiple
}

// a1111ImageCount is batch_size x n_iter, each at least 1, capped.
func a1111ImageCount(batchSize, nIter int) int {
	return min(max(batchSize, 1)*max(nIter, 1), a1111MaxImages)
}

// a1111Model picks the lane: the checkpoint the client asked for, else the
// configured default.
func (p *Proxy) a1111ModelFor(in a1111Txt2ImgRequest) string {
	if name, ok := in.OverrideSettings["sd_model_checkpoint"].(string); ok && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return p.a1111Model
}

// a1111LaneRequest translates a txt2img payload into the lane request.
func (p *Proxy) a1111LaneRequest(in a1111Txt2ImgRequest) laneImageRequest {
	out := laneImageRequest{
		Model:          p.a1111ModelFor(in),
		Prompt:         in.Prompt,
		NegativePrompt: in.NegativePrompt,
		Size:           fmt.Sprintf("%dx%d", a1111Side(in.Width), a1111Side(in.Height)),
		N:              a1111ImageCount(in.BatchSize, in.NIter),
		ResponseFormat: "b64_json",
	}
	// The WebUI uses -1 for "random"; the lane uses an absent seed.
	if in.Seed != nil && *in.Seed >= 0 {
		out.Seed = in.Seed
	}
	return out
}

// a1111LaneHTTPRequest is the image request handed to handleRequest: the
// caller's context (so a dropped connection cancels the render wait) and
// headers (auth, request ID, QoS class), with the lane's path and body.
func a1111LaneHTTPRequest(r *http.Request, body []byte) *http.Request {
	req := r.Clone(r.Context())
	req.Method = http.MethodPost
	req.URL.Path = pathImagesGenerations
	req.URL.RawPath = ""
	req.URL.RawQuery = ""
	req.RequestURI = ""
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Del("Content-Length")
	req.Header.Del("Content-Encoding")
	return req
}

// a1111Gate answers for every /sdapi route when the adapter is off or the
// caller is not authorized. It reports whether the handler may continue.
func (p *Proxy) a1111Gate(w http.ResponseWriter, r *http.Request) bool {
	if p.a1111Model == "" {
		validation.WriteServiceUnavailable(w, "AUTOMATIC1111 image API not configured (FLEXINFER_A1111_MODEL unset)")
		return false
	}
	if !p.checkAuth(r) {
		validation.WriteUnauthorized(w, "Invalid or missing bearer token")
		return false
	}
	return true
}

// handleA1111Samplers is GET /sdapi/v1/samplers. Clients use it as the
// readiness probe (Talemate) and to fill a dropdown. The lane picks its own
// sampler, so there is one entry.
func (p *Proxy) handleA1111Samplers(w http.ResponseWriter, r *http.Request) {
	if !p.a1111Gate(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{
		{"name": "Lane default", "aliases": []string{}, "options": map[string]any{}},
	})
}

// handleA1111Schedulers is GET /sdapi/v1/schedulers.
func (p *Proxy) handleA1111Schedulers(w http.ResponseWriter, r *http.Request) {
	if !p.a1111Gate(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{
		{"name": "automatic", "label": "Automatic", "aliases": nil},
	})
}

// handleA1111Models is GET /sdapi/v1/sd-models: the default lane, under the
// name a client would send back as sd_model_checkpoint.
func (p *Proxy) handleA1111Models(w http.ResponseWriter, r *http.Request) {
	if !p.a1111Gate(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{
		{"title": p.a1111Model, "model_name": p.a1111Model},
	})
}

// handleA1111Options is GET /sdapi/v1/options: the one option clients read.
func (p *Proxy) handleA1111Options(w http.ResponseWriter, r *http.Request) {
	if !p.a1111Gate(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sd_model_checkpoint": p.a1111Model})
}

// handleA1111Interrupt is POST /sdapi/v1/interrupt. A client that cancels
// also drops its txt2img connection, which already cancels the proxied
// request, so there is nothing more to do here. The WebUI's interrupt is
// global; cancelling other callers' renders from here would be wrong.
func (p *Proxy) handleA1111Interrupt(w http.ResponseWriter, r *http.Request) {
	if !p.a1111Gate(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleA1111Txt2Img is POST /sdapi/v1/txt2img.
func (p *Proxy) handleA1111Txt2Img(w http.ResponseWriter, r *http.Request) {
	if !p.a1111Gate(w, r) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a1111MaxRequestBytes))
	if err != nil {
		validation.WriteBadRequest(w, "reading txt2img request: "+err.Error())
		return
	}
	var in a1111Txt2ImgRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		validation.WriteBadRequest(w, "invalid txt2img request: "+err.Error())
		return
	}
	if strings.TrimSpace(in.Prompt) == "" {
		validation.WriteMissingField(w, "prompt")
		return
	}
	lane := p.a1111LaneRequest(in)
	body, err := json.Marshal(lane)
	if err != nil {
		validation.WriteInternalError(w, "encoding image request: "+err.Error())
		return
	}

	req := a1111LaneHTTPRequest(r, body)

	buf := &bufferedResponse{header: http.Header{}}
	dispatch := p.a1111Dispatch
	if dispatch == nil {
		dispatch = p.handleRequest
	}
	dispatch(buf, req)

	if id := buf.header.Get("X-Request-ID"); id != "" {
		w.Header().Set("X-Request-ID", id)
	}
	status := buf.statusOrOK()
	if status != http.StatusOK {
		// Hand the proxy's or the lane's own error back unchanged: it already
		// says why (lane not ready, card held, validation).
		if ct := buf.header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		if ra := buf.header.Get("Retry-After"); ra != "" {
			w.Header().Set("Retry-After", ra)
		}
		w.WriteHeader(status)
		if _, err := w.Write(buf.body.Bytes()); err != nil {
			slog.Debug("a1111 error write failed", "error", err)
		}
		return
	}
	var out laneImageResponse
	if err := json.Unmarshal(buf.body.Bytes(), &out); err != nil {
		validation.WriteError(w, http.StatusBadGateway, "image lane returned a response that is not JSON: "+err.Error(),
			validation.ErrorTypeServer, "a1111_bad_lane_response")
		return
	}
	images := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		if d.B64JSON != "" {
			images = append(images, d.B64JSON)
		}
	}
	if len(images) == 0 {
		validation.WriteError(w, http.StatusBadGateway, "image lane returned no image",
			validation.ErrorTypeServer, "a1111_no_image")
		return
	}
	params := map[string]any{
		"prompt":          lane.Prompt,
		"negative_prompt": lane.NegativePrompt,
		"width":           a1111Side(in.Width),
		"height":          a1111Side(in.Height),
		"batch_size":      lane.N,
		"sd_model_name":   lane.Model,
		// The lane chose these; the client's values were not applied.
		"sampler_name": "Lane default",
	}
	if lane.Seed != nil {
		params["seed"] = *lane.Seed
	}
	info, err := json.Marshal(params)
	if err != nil {
		info = []byte("{}")
	}
	writeJSON(w, http.StatusOK, a1111Txt2ImgResponse{Images: images, Parameters: params, Info: string(info)})
}

// bufferedResponse collects a dispatched handler's response in memory so it
// can be reshaped. Image responses are a few megabytes of base64 at most.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// Flush lets handlers that flush after writing run unchanged.
func (b *bufferedResponse) Flush() {}

func (b *bufferedResponse) statusOrOK() int {
	if b.status == 0 {
		return http.StatusOK
	}
	return b.status
}
