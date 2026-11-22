package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fakeMP4 = []byte("\x00\x00\x00\x18ftypisom-not-really-a-clip")

func newVideoFixture(t *testing.T) *jobFixture {
	t.Helper()
	f := newJobFixture(t,
		readyLane("wan21-vace", "diffusers", map[string]string{constants.AnnotationServePaths: pathVideosGenerations}),
		readyLane("qwen-image", "diffusers", map[string]string{constants.AnnotationServePaths: "/v1/images/generations"}),
	)
	reply, _ := json.Marshal(map[string]any{"created": 1, "data": []map[string]any{{
		"b64_json": base64.StdEncoding.EncodeToString(fakeMP4), "mime_type": "video/mp4",
		"width": 832, "height": 480, "num_frames": 33, "fps": 16,
	}}})
	f.replies.Store("wan21-vace", string(reply))
	return f
}

func (f *jobFixture) doVideo(t *testing.T, method, path string, body []byte, contentType string) (*httptest.ResponseRecorder, videoObject) {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", contentType)
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	f.p.handler().ServeHTTP(rec, r)
	var v videoObject
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
	}
	return rec, v
}

// canvasVideoForm is what Infinite Canvas sends for an image-to-video node.
func canvasVideoForm(t *testing.T, model string, image []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range map[string]string{
		"model": model, "prompt": "the lighthouse beam sweeps the fog", "seconds": "6", "size": "1280x720",
		"ratio": "16:9", "resolution_name": "720p", "generate_audio": "true", "watermark": "false", "mode": "frames",
	} {
		require.NoError(t, mw.WriteField(k, v))
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="first_frame"; filename="first.png"`)
	h.Set("Content-Type", "image/png")
	part, err := mw.CreatePart(h)
	require.NoError(t, err)
	_, _ = part.Write(image)
	audio, err := mw.CreateFormFile("audio[]", "ref.mp3")
	require.NoError(t, err)
	_, _ = audio.Write([]byte("ID3 ignored"))
	require.NoError(t, mw.Close())
	return buf.Bytes(), mw.FormDataContentType()
}

func TestVideoAliasRunsACanvasImageToVideoRequest(t *testing.T) {
	f := newVideoFixture(t)
	png := []byte("\x89PNG\r\n\x1a\nframe")
	body, ct := canvasVideoForm(t, "wan21-vace", png)

	rec, v := f.doVideo(t, http.MethodPost, "/v1/videos", body, ct)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "video", v.Object)
	assert.Equal(t, "queued", v.Status)
	assert.Equal(t, "wan21-vace", v.Model)
	assert.Equal(t, "832x480", v.Size, "1280x720 snaps to the lane's landscape size")
	assert.Equal(t, "2", v.Seconds, "6 s is capped at 33 frames, which is 2 s at 16 fps")
	assert.Nil(t, v.Error)

	j := f.waitJob(t, v.ID)
	require.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase)
	assert.Equal(t, pathVideosGenerations, j.Spec.Path)

	var sent laneVideoRequest
	require.NoError(t, json.Unmarshal(<-f.bodies, &sent))
	assert.Equal(t, "832x480", sent.Size)
	assert.Equal(t, 33, sent.NumFrames)
	assert.Equal(t, 16, sent.FPS)
	assert.Equal(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png), sent.Image)

	rec, got := f.doVideo(t, http.MethodGet, "/v1/videos/"+v.ID, nil, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "completed", got.Status)
	assert.EqualValues(t, 100, got.Progress)
	assert.NotNil(t, got.CompletedAt)

	rec, _ = f.doVideo(t, http.MethodGet, "/v1/videos/"+v.ID+"/content", nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "video/mp4", rec.Header().Get("Content-Type"))
	assert.Equal(t, fakeMP4, rec.Body.Bytes())

	rec, _ = f.doVideo(t, http.MethodGet, "/v1/videos/"+v.ID+"/content?variant=thumbnail", nil, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestVideoAliasAcceptsJSONAndKeepsExplicitFrames(t *testing.T) {
	f := newVideoFixture(t)
	rec, v := f.doVideo(t, http.MethodPost, "/v1/videos",
		[]byte(`{"model":"wan21-vace","prompt":"waves","seconds":"8","size":"480x832","num_frames":50,"seed":7}`), "application/json")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "480x832", v.Size)
	assert.Equal(t, "3", v.Seconds, "an explicit num_frames is kept, rounded down to 4k+1 (49 frames)")

	f.waitJob(t, v.ID)
	var sent laneVideoRequest
	require.NoError(t, json.Unmarshal(<-f.bodies, &sent))
	assert.Equal(t, 49, sent.NumFrames)
	require.NotNil(t, sent.Seed)
	assert.EqualValues(t, 7, *sent.Seed)
	assert.Empty(t, sent.Image)
}

func TestVideoAliasRefusesWhatItCannotServe(t *testing.T) {
	f := newVideoFixture(t)

	rec, _ := f.doVideo(t, http.MethodPost, "/v1/videos", []byte(`{"model":"nope","prompt":"x"}`), "application/json")
	assert.Equal(t, http.StatusNotFound, rec.Code, "an unknown model is refused at submission")

	rec, _ = f.doVideo(t, http.MethodPost, "/v1/videos", []byte(`{"model":"qwen-image","prompt":"x"}`), "application/json")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a lane that does not serve video is refused")

	rec, _ = f.doVideo(t, http.MethodPost, "/v1/videos", []byte(`{"model":"wan21-vace"}`), "application/json")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "prompt is required")

	rec, _ = f.doVideo(t, http.MethodPost, "/v1/videos", []byte(`model=x`), "application/x-www-form-urlencoded")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// An image job is not visible through the video alias.
	_, job := f.do(t, http.MethodPost, "/v1/jobs",
		strings.NewReader(`{"path":"/v1/images/generations","request":{"model":"qwen-image","prompt":"cat"}}`), nil)
	require.NotEmpty(t, job.ID)
	rec, _ = f.doVideo(t, http.MethodGet, "/v1/videos/"+job.ID, nil, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestVideoAliasContentBeforeCompletionAndDelete(t *testing.T) {
	f := newVideoFixture(t)
	rec, v := f.doVideo(t, http.MethodPost, "/v1/videos", []byte(`{"model":"wan21-vace","prompt":"rain"}`), "application/json")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec, _ = f.doVideo(t, http.MethodGet, "/v1/videos/"+v.ID+"/content", nil, "")
	assert.Equal(t, http.StatusConflict, rec.Code, "no content while queued")

	rec, _ = f.doVideo(t, http.MethodDelete, "/v1/videos/"+v.ID, nil, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"id":"`+v.ID+`","object":"video.deleted","deleted":true}`, rec.Body.String())
	j := f.waitJob(t, v.ID)
	assert.Equal(t, aiv1alpha2.InferenceJobCancelled, j.Status.Phase, "deleting a queued clip cancels it")
}

func TestVideoLaneSize(t *testing.T) {
	for in, want := range map[string]string{
		"":          "832x480",
		"auto":      "832x480",
		"1280x720":  "832x480",
		"720x1280":  "480x832",
		"1024x1024": "624x624",
		"832x480":   "832x480",
		"640x368":   "640x368",
		"641x368":   "832x480",
		"0x10":      "832x480",
	} {
		assert.Equal(t, want, videoLaneSize(in), in)
	}
}

func TestVideoLaneFrames(t *testing.T) {
	type tc struct {
		seconds          string
		numFrames, fps   int
		wantFrames, wFPS int
	}
	for _, c := range []tc{
		{"4", 0, 0, 33, 16},
		{"1", 0, 0, 17, 16},
		{"30", 0, 0, 33, 16},
		{"", 0, 0, 33, 16},
		{"junk", 0, 0, 33, 16},
		{"0.1", 0, 0, 5, 16},
		{"4", 81, 0, 81, 16},
		{"4", 200, 0, 81, 16},
		{"4", 50, 0, 49, 16},
		{"2", 0, 8, 17, 8},
		{"4", 0, 99, 33, 16},
	} {
		frames, fps := videoLaneFrames(c.seconds, c.numFrames, c.fps)
		assert.Equal(t, c.wantFrames, frames, "%+v", c)
		assert.Equal(t, c.wFPS, fps, "%+v", c)
		assert.Equal(t, 1, (frames-1)%4+1, "frames are 4k+1")
	}
}
