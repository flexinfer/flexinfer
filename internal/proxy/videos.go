package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Videos alias: the OpenAI Videos API shape (POST /v1/videos, GET
// /v1/videos/{id}, GET /v1/videos/{id}/content, DELETE /v1/videos/{id}) on
// top of the job runner. OpenAI removed that API on 2026-09-24, but creative
// frontends (Infinite Canvas and others) still speak it, so a video lane is
// reachable from them without an adapter. Each create becomes an
// InferenceJob on /v1/videos/generations; the alias only translates the
// request and renders the job as a video object.

const (
	pathVideosGenerations = "/v1/videos/generations"
	videoObjectType       = "video"
	codeVideoNotFound     = "video_not_found"

	jobAnnotationVideoSeconds = "flexinfer.ai/video-seconds"
	jobAnnotationVideoSize    = "flexinfer.ai/video-size"

	// The Wan lanes render at most 832x480 (or 480x832) in 16 fps frames of
	// 4k+1. Without an explicit num_frames the alias asks for at most 33
	// frames (about 2 s): a warm 1.3B lane renders that in about two
	// minutes, inside the ~5 minute poll budget these frontends give a clip.
	videoAliasFPS          = 16
	videoAliasMaxFrames    = 33
	videoAliasMinFrames    = 5
	videoLaneMaxFrames     = 81
	videoLaneMaxArea       = 832 * 480
	videoAliasMaxImageSize = 12 << 20
)

// videoObject is the OpenAI video object, plus the job's lane detail.
type videoObject struct {
	ID          string       `json:"id"`
	Object      string       `json:"object"`
	Model       string       `json:"model"`
	Status      string       `json:"status"`
	Progress    int32        `json:"progress"`
	CreatedAt   int64        `json:"created_at"`
	CompletedAt *int64       `json:"completed_at"`
	ExpiresAt   *int64       `json:"expires_at"`
	Seconds     string       `json:"seconds,omitempty"`
	Size        string       `json:"size,omitempty"`
	Error       *videoError  `json:"error"`
	FlexInfer   jobFlexInfer `json:"flexinfer"`
}

type videoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// videoView renders a job created by the alias as a video object. The
// "incomplete" phase (output over the size cap) has no video counterpart and
// reads as failed.
func videoView(j *aiv1alpha2.InferenceJob, job jobObject) videoObject {
	status := job.Status
	if status == string(aiv1alpha2.InferenceJobIncomplete) {
		status = string(aiv1alpha2.InferenceJobFailed)
	}
	v := videoObject{
		ID: job.ID, Object: videoObjectType, Model: job.Model, Status: status, Progress: job.Progress,
		CreatedAt: job.CreatedAt, CompletedAt: job.CompletedAt, ExpiresAt: job.ExpiresAt,
		Seconds: j.Annotations[jobAnnotationVideoSeconds], Size: j.Annotations[jobAnnotationVideoSize],
		FlexInfer: job.FlexInfer,
	}
	if job.Error != nil {
		v.Error = &videoError{Code: job.Error.Code, Message: job.Error.Message}
	} else if status == string(aiv1alpha2.InferenceJobFailed) {
		v.Error = &videoError{Code: "video_incomplete", Message: "the clip did not finish"}
	}
	return v
}

// videoRequest is what the alias reads from a create, multipart or JSON.
type videoRequest struct {
	model, prompt, negativePrompt string
	seconds, size                 string
	numFrames, fps, steps         int
	seed                          *int64
	// image is the conditioning frame for image-to-video lanes, as a data URI.
	image string
}

// laneVideoRequest is the /v1/videos/generations body the alias sends.
type laneVideoRequest struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	NegativePrompt    string `json:"negative_prompt,omitempty"`
	Size              string `json:"size"`
	NumFrames         int    `json:"num_frames"`
	FPS               int    `json:"fps"`
	NumInferenceSteps int    `json:"num_inference_steps,omitempty"`
	Seed              *int64 `json:"seed,omitempty"`
	Image             string `json:"image,omitempty"`
}

// handleCreateVideo is POST /v1/videos.
func (p *Proxy) handleCreateVideo(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	in, err := readVideoRequest(r)
	if err != nil {
		validation.WriteBadRequest(w, "invalid video request: "+err.Error())
		return
	}
	if in.model == "" {
		validation.WriteMissingField(w, "model")
		return
	}
	if strings.TrimSpace(in.prompt) == "" {
		validation.WriteMissingField(w, "prompt")
		return
	}
	size := videoLaneSize(in.size)
	frames, fps := videoLaneFrames(in.seconds, in.numFrames, in.fps)
	body, err := json.Marshal(laneVideoRequest{
		Model: in.model, Prompt: in.prompt, NegativePrompt: in.negativePrompt, Size: size,
		NumFrames: frames, FPS: fps, NumInferenceSteps: in.steps, Seed: in.seed, Image: in.image,
	})
	if err != nil {
		validation.WriteInternalError(w, "encoding video request: "+err.Error())
		return
	}
	seconds := strconv.FormatFloat(float64(frames-1)/float64(fps), 'f', -1, 64)
	job, ok := p.createJob(w, r, jobSubmission{
		model: in.model, path: pathVideosGenerations, body: body, contentType: "application/json",
		idempotencyKey: r.Header.Get(headerIdempotencyKey),
		annotations:    map[string]string{jobAnnotationVideoSeconds: seconds, jobAnnotationVideoSize: size},
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, videoView(job, p.describeJob(r.Context(), job)))
}

// handleGetVideo is GET /v1/videos/{id}.
func (p *Proxy) handleGetVideo(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	j, ok := p.lookupVideoJob(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, videoView(j, p.describeJob(r.Context(), j)))
}

// handleVideoContent is GET /v1/videos/{id}/content: the MP4 itself, decoded
// from the lane's stored b64_json response. Only the default "video" variant
// exists; the lanes make no thumbnail or spritesheet.
func (p *Proxy) handleVideoContent(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	if v := r.URL.Query().Get("variant"); v != "" && v != "video" {
		validation.WriteError(w, http.StatusNotFound, "variant "+v+" is not produced by this lane",
			validation.ErrorTypeNotFound, codeVideoNotFound)
		return
	}
	j, ok := p.lookupVideoJob(w, r)
	if !ok {
		return
	}
	if j.Status.Phase != aiv1alpha2.InferenceJobCompleted || j.Status.Output == nil {
		validation.WriteError(w, http.StatusConflict,
			fmt.Sprintf("video %s is %s; content is available once it is completed", j.Name, jobView(j, nil, nil).Status),
			validation.ErrorTypeInvalidRequest, codeJobNotCompleted)
		return
	}
	stored, err := p.jobs.cfg.Store.Get(r.Context(), j.Status.Output.URI)
	if errors.Is(err, errJobObjectNotFound) {
		validation.WriteError(w, http.StatusGone, "video output no longer stored", validation.ErrorTypeNotFound, codeVideoNotFound)
		return
	}
	if err != nil {
		validation.WriteInternalError(w, "reading video output: "+err.Error())
		return
	}
	defer func() { _ = stored.Close() }()
	clip, mimeType, err := decodeLaneVideo(stored)
	if err != nil {
		validation.WriteInternalError(w, "decoding video output: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(clip)))
	if j.Status.Output.SHA256 != "" {
		w.Header().Set("ETag", `W/"`+j.Status.Output.SHA256+`"`)
	}
	if _, err := w.Write(clip); err != nil {
		slog.Debug("video content write failed", "job", j.Name, "error", err)
	}
}

// handleDeleteVideo is DELETE /v1/videos/{id}: a running clip is cancelled
// (the runner then expires it on its TTL), a finished one is deleted with
// its stored output.
func (p *Proxy) handleDeleteVideo(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	j, ok := p.lookupVideoJob(w, r)
	if !ok {
		return
	}
	if j.Status.Phase.IsTerminal() {
		if err := p.client.Delete(r.Context(), j); err != nil && client.IgnoreNotFound(err) != nil {
			validation.WriteInternalError(w, "deleting video: "+err.Error())
			return
		}
	} else if !j.Spec.Cancel {
		patch := client.MergeFrom(j.DeepCopy())
		j.Spec.Cancel = true
		if err := p.client.Patch(r.Context(), j, patch); err != nil {
			validation.WriteInternalError(w, "cancelling video: "+err.Error())
			return
		}
		p.jobs.wake()
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": j.Name, "object": "video.deleted", "deleted": true})
}

// lookupVideoJob is lookupJob limited to jobs on the video path, so the alias
// does not expose chat or image jobs as videos.
func (p *Proxy) lookupVideoJob(w http.ResponseWriter, r *http.Request) (*aiv1alpha2.InferenceJob, bool) {
	j, ok := p.lookupJob(w, r)
	if !ok {
		return nil, false
	}
	if j.Spec.Path != pathVideosGenerations {
		validation.WriteError(w, http.StatusNotFound, "video "+j.Name+" not found", validation.ErrorTypeNotFound, codeVideoNotFound)
		return nil, false
	}
	return j, true
}

// readVideoRequest reads a create in either encoding. Multipart is what the
// OpenAI SDKs and the canvas frontends send; JSON is accepted too.
func readVideoRequest(r *http.Request) (videoRequest, error) {
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	body := io.LimitReader(r.Body, maxJobSubmitBytes)
	switch mediaType {
	case "multipart/form-data":
		return readVideoMultipart(body, params["boundary"])
	case "application/json", "":
		return readVideoJSON(body)
	default:
		return videoRequest{}, fmt.Errorf("unsupported Content-Type %q (use multipart/form-data or application/json)", mediaType)
	}
}

// videoImageFields are the conditioning-image field names, first match wins:
// OpenAI's input_reference, then the canvas frontends' first_frame, image,
// image[] and images.
var videoImageFields = []string{"input_reference", "first_frame", "image", "image[]", "images"}

func readVideoMultipart(body io.Reader, boundary string) (videoRequest, error) {
	if boundary == "" {
		return videoRequest{}, errors.New("multipart body without a boundary")
	}
	fields := map[string]string{}
	images := map[string]string{}
	mr := multipart.NewReader(body, boundary)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return videoRequest{}, err
		}
		name := part.FormName()
		if part.FileName() == "" {
			b, err := io.ReadAll(io.LimitReader(part, 64<<10))
			if err != nil {
				return videoRequest{}, err
			}
			if _, seen := fields[name]; !seen {
				fields[name] = string(b)
			}
			continue
		}
		if !isVideoImageField(name) {
			continue // reference videos and audio have no lane input
		}
		if _, seen := images[name]; seen {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(part, videoAliasMaxImageSize+1))
		if err != nil {
			return videoRequest{}, err
		}
		if len(b) > videoAliasMaxImageSize {
			return videoRequest{}, fmt.Errorf("%s exceeds %d MiB", name, videoAliasMaxImageSize>>20)
		}
		ct := part.Header.Get("Content-Type")
		if ct == "" || ct == "application/octet-stream" {
			ct = http.DetectContentType(b)
		}
		if !strings.HasPrefix(ct, "image/") {
			return videoRequest{}, fmt.Errorf("%s must be an image, got %s", name, ct)
		}
		images[name] = "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(b)
	}
	in := videoRequest{
		model: fields["model"], prompt: fields["prompt"], negativePrompt: fields["negative_prompt"],
		seconds: fields["seconds"], size: fields["size"],
	}
	var err error
	if in.numFrames, err = optionalInt(fields, "num_frames"); err != nil {
		return in, err
	}
	if in.fps, err = optionalInt(fields, "fps"); err != nil {
		return in, err
	}
	if in.steps, err = optionalInt(fields, "num_inference_steps"); err != nil {
		return in, err
	}
	if s := strings.TrimSpace(fields["seed"]); s != "" {
		seed, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return in, fmt.Errorf("seed: %w", err)
		}
		in.seed = &seed
	}
	for _, name := range videoImageFields {
		if img, ok := images[name]; ok {
			in.image = img
			break
		}
	}
	return in, nil
}

func isVideoImageField(name string) bool {
	for _, f := range videoImageFields {
		if f == name {
			return true
		}
	}
	return false
}

func optionalInt(fields map[string]string, name string) (int, error) {
	s := strings.TrimSpace(fields[name])
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

// videoJSONRequest is the JSON create. seconds may be a string (OpenAI) or a
// number; input_reference and image are data URIs or bare base64.
type videoJSONRequest struct {
	Model             string          `json:"model"`
	Prompt            string          `json:"prompt"`
	NegativePrompt    string          `json:"negative_prompt"`
	Seconds           json.RawMessage `json:"seconds"`
	Size              string          `json:"size"`
	NumFrames         int             `json:"num_frames"`
	FPS               int             `json:"fps"`
	NumInferenceSteps int             `json:"num_inference_steps"`
	Seed              *int64          `json:"seed"`
	InputReference    string          `json:"input_reference"`
	Image             string          `json:"image"`
}

func readVideoJSON(body io.Reader) (videoRequest, error) {
	var in videoJSONRequest
	if err := json.NewDecoder(body).Decode(&in); err != nil {
		return videoRequest{}, err
	}
	seconds := strings.Trim(string(bytes.TrimSpace(in.Seconds)), `"`)
	img := in.InputReference
	if img == "" {
		img = in.Image
	}
	if len(img) > videoAliasMaxImageSize*4/3+64 {
		return videoRequest{}, fmt.Errorf("input_reference exceeds %d MiB", videoAliasMaxImageSize>>20)
	}
	return videoRequest{
		model: in.Model, prompt: in.Prompt, negativePrompt: in.NegativePrompt, seconds: seconds, size: in.Size,
		numFrames: in.NumFrames, fps: in.FPS, steps: in.NumInferenceSteps, seed: in.Seed, image: img,
	}, nil
}

// videoLaneSize maps a requested WIDTHxHEIGHT onto what the lanes render. A
// size that already fits (area at most 832x480, both sides multiples of 16)
// is kept; anything else snaps by orientation to the sizes Wan was trained
// on: 832x480, 480x832, or 624x624 for near-square requests. No size means
// the lane default, 832x480.
func videoLaneSize(requested string) string {
	w, h, ok := parseVideoSize(requested)
	if !ok {
		return "832x480"
	}
	if w*h <= videoLaneMaxArea && w%16 == 0 && h%16 == 0 {
		return fmt.Sprintf("%dx%d", w, h)
	}
	switch ratio := float64(w) / float64(h); {
	case ratio >= 1.2:
		return "832x480"
	case ratio <= 1/1.2:
		return "480x832"
	default:
		return "624x624"
	}
}

func parseVideoSize(s string) (int, int, bool) {
	ws, hs, found := strings.Cut(strings.ToLower(strings.TrimSpace(s)), "x")
	if !found {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// videoLaneFrames picks num_frames and fps. An explicit num_frames is kept
// (rounded down to 4k+1, capped at the lane's 81); otherwise seconds at 16
// fps, capped at videoAliasMaxFrames so a clip fits the frontends' poll
// budget. A request for longer gets the capped clip, and the video object
// reports the seconds actually rendered.
func videoLaneFrames(seconds string, numFrames, fps int) (int, int) {
	if fps <= 0 || fps > 24 {
		fps = videoAliasFPS
	}
	frames := numFrames
	limit := videoLaneMaxFrames
	if frames <= 0 {
		secs, err := strconv.ParseFloat(strings.TrimSpace(seconds), 64)
		if err != nil || secs <= 0 || math.IsNaN(secs) || math.IsInf(secs, 0) {
			secs = 4
		}
		frames = int(math.Round(secs*float64(fps))) + 1
		limit = videoAliasMaxFrames
	}
	frames = min(frames, limit)
	frames = max(frames, videoAliasMinFrames)
	frames = (frames-1)/4*4 + 1
	return frames, fps
}

// decodeLaneVideo pulls the clip out of a stored /v1/videos/generations
// response: {"data":[{"b64_json": ..., "mime_type": ...}]}.
func decodeLaneVideo(r io.Reader) ([]byte, string, error) {
	var resp struct {
		Data []struct {
			B64JSON  string `json:"b64_json"`
			MimeType string `json:"mime_type"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return nil, "", err
	}
	if len(resp.Data) == 0 || resp.Data[0].B64JSON == "" {
		return nil, "", errors.New("the lane response holds no clip")
	}
	clip, err := base64.StdEncoding.DecodeString(resp.Data[0].B64JSON)
	if err != nil {
		return nil, "", err
	}
	mimeType := resp.Data[0].MimeType
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	return clip, mimeType, nil
}
