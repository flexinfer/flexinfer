package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// a1111TestProxy serves the /sdapi routes over a fake image lane that records
// the request it was handed and answers with reply.
type a1111Lane struct {
	calls  int
	method string
	path   string
	header http.Header
	body   map[string]any
	reply  func(w http.ResponseWriter)
}

func newA1111TestProxy(model string) (*Proxy, *a1111Lane) {
	lane := &a1111Lane{reply: func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "req-1")
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"aW1hZ2Ux"}]}`)
	}}
	p := &Proxy{a1111Model: model}
	p.a1111Dispatch = func(w http.ResponseWriter, r *http.Request) {
		lane.calls++
		lane.method, lane.path, lane.header = r.Method, r.URL.Path, r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		lane.body = map[string]any{}
		_ = json.Unmarshal(raw, &lane.body)
		lane.reply(w)
	}
	return p, lane
}

func a1111Do(t *testing.T, p *Proxy, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	p.newServeMux().ServeHTTP(rec, req)
	return rec
}

func TestA1111RoutesAnswer503WithoutADefaultModel(t *testing.T) {
	p, lane := newA1111TestProxy("")
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/sdapi/v1/samplers"},
		{http.MethodGet, "/sdapi/v1/schedulers"},
		{http.MethodGet, "/sdapi/v1/sd-models"},
		{http.MethodGet, "/sdapi/v1/options"},
		{http.MethodPost, "/sdapi/v1/interrupt"},
		{http.MethodPost, "/sdapi/v1/txt2img"},
	} {
		rec := a1111Do(t, p, c.method, c.path, `{"prompt":"a harbor"}`, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", c.method, c.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "FLEXINFER_A1111_MODEL") {
			t.Errorf("%s %s body does not name the setting: %s", c.method, c.path, rec.Body.String())
		}
	}
	if lane.calls != 0 {
		t.Fatalf("lane was called %d times while the adapter is off", lane.calls)
	}
}

func TestA1111RoutesRequireTheProxyToken(t *testing.T) {
	p, lane := newA1111TestProxy("qwen-image")
	p.authEnabled, p.authToken = true, "secret"
	if rec := a1111Do(t, p, http.MethodGet, "/sdapi/v1/samplers", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("samplers without a token = %d, want 401", rec.Code)
	}
	rec := a1111Do(t, p, http.MethodPost, "/sdapi/v1/txt2img", `{"prompt":"x"}`, map[string]string{"Authorization": "Bearer wrong"})
	if rec.Code != http.StatusUnauthorized || lane.calls != 0 {
		t.Fatalf("txt2img with a wrong token = %d (lane calls %d), want 401 and no call", rec.Code, lane.calls)
	}
	rec = a1111Do(t, p, http.MethodPost, "/sdapi/v1/txt2img", `{"prompt":"x"}`, map[string]string{"Authorization": "Bearer secret"})
	if rec.Code != http.StatusOK || lane.header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("txt2img with the token = %d, forwarded Authorization %q", rec.Code, lane.header.Get("Authorization"))
	}
}

func TestA1111ReadOnlyRoutesDescribeTheDefaultLane(t *testing.T) {
	p, _ := newA1111TestProxy("qwen-image")

	var samplers []map[string]any
	rec := a1111Do(t, p, http.MethodGet, "/sdapi/v1/samplers", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &samplers); rec.Code != http.StatusOK || err != nil || len(samplers) != 1 {
		t.Fatalf("samplers = %d %s (%v)", rec.Code, rec.Body.String(), err)
	}

	var models []map[string]any
	rec = a1111Do(t, p, http.MethodGet, "/sdapi/v1/sd-models", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &models); err != nil || len(models) != 1 || models[0]["title"] != "qwen-image" {
		t.Fatalf("sd-models = %s (%v)", rec.Body.String(), err)
	}

	var options map[string]any
	rec = a1111Do(t, p, http.MethodGet, "/sdapi/v1/options", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &options); err != nil || options["sd_model_checkpoint"] != "qwen-image" {
		t.Fatalf("options = %s (%v)", rec.Body.String(), err)
	}

	if rec = a1111Do(t, p, http.MethodPost, "/sdapi/v1/interrupt", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("interrupt = %d, want 200", rec.Code)
	}
}

func TestA1111Txt2ImgBecomesALaneImageRequest(t *testing.T) {
	p, lane := newA1111TestProxy("qwen-image")
	// What Talemate sends: SD sampler settings on every request.
	body := `{"prompt":"a rainy harbor","negative_prompt":"blurry","steps":40,"width":832,"height":1216,
	          "cfg_scale":7,"sampler_name":"DPM++ 2M","scheduler":"Automatic","seed":42}`
	rec := a1111Do(t, p, http.MethodPost, "/sdapi/v1/txt2img", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("txt2img = %d %s", rec.Code, rec.Body.String())
	}
	if lane.method != http.MethodPost || lane.path != "/v1/images/generations" {
		t.Fatalf("dispatched %s %s, want POST /v1/images/generations", lane.method, lane.path)
	}
	if got := lane.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("dispatched Content-Type = %q", got)
	}
	want := map[string]any{
		"model": "qwen-image", "prompt": "a rainy harbor", "negative_prompt": "blurry",
		"size": "832x1216", "n": float64(1), "seed": float64(42), "response_format": "b64_json",
	}
	for k, v := range want {
		if lane.body[k] != v {
			t.Errorf("lane body %s = %v, want %v", k, lane.body[k], v)
		}
	}
	// The client's Stable Diffusion sampler settings must not reach the lane.
	for _, k := range []string{"steps", "num_inference_steps", "cfg_scale", "guidance_scale", "true_cfg_scale", "sampler_name", "scheduler"} {
		if _, present := lane.body[k]; present {
			t.Errorf("lane body carries %s = %v; the lane's own defaults should apply", k, lane.body[k])
		}
	}

	var out struct {
		Images     []string       `json:"images"`
		Parameters map[string]any `json:"parameters"`
		Info       string         `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(out.Images) != 1 || out.Images[0] != "aW1hZ2Ux" {
		t.Fatalf("images = %v, want the lane's base64 image", out.Images)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(out.Info), &info); err != nil {
		t.Fatalf("info is not a JSON string, as WebUI clients expect: %q (%v)", out.Info, err)
	}
	if out.Parameters["sd_model_name"] != "qwen-image" || info["width"] != float64(832) || info["height"] != float64(1216) {
		t.Errorf("parameters = %v, info = %v", out.Parameters, info)
	}
	if rec.Header().Get("X-Request-ID") != "req-1" {
		t.Errorf("X-Request-ID not carried over: %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestA1111Txt2ImgSizesSeedsAndBatches(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		wantSize   string
		wantN      float64
		wantSeed   any // nil = the lane request has no seed
	}{
		{name: "defaults", body: `{"prompt":"x"}`, wantSize: "1024x1024", wantN: 1},
		{name: "random seed is dropped", body: `{"prompt":"x","seed":-1}`, wantSize: "1024x1024", wantN: 1},
		{name: "seed zero is kept", body: `{"prompt":"x","seed":0}`, wantSize: "1024x1024", wantN: 1, wantSeed: float64(0)},
		{name: "rounded down to 16", body: `{"prompt":"x","width":1000,"height":700}`, wantSize: "992x688", wantN: 1},
		{name: "clamped", body: `{"prompt":"x","width":5000,"height":64}`, wantSize: "2048x256", wantN: 1},
		{name: "batch times iterations", body: `{"prompt":"x","batch_size":2,"n_iter":2}`, wantSize: "1024x1024", wantN: 4},
		{name: "batch is capped", body: `{"prompt":"x","batch_size":8,"n_iter":3}`, wantSize: "1024x1024", wantN: 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, lane := newA1111TestProxy("qwen-image")
			if rec := a1111Do(t, p, http.MethodPost, "/sdapi/v1/txt2img", tt.body, nil); rec.Code != http.StatusOK {
				t.Fatalf("txt2img = %d %s", rec.Code, rec.Body.String())
			}
			if lane.body["size"] != tt.wantSize || lane.body["n"] != tt.wantN {
				t.Errorf("size %v n %v, want %s %v", lane.body["size"], lane.body["n"], tt.wantSize, tt.wantN)
			}
			if seed, present := lane.body["seed"]; (tt.wantSeed == nil) == present || (present && seed != tt.wantSeed) {
				t.Errorf("seed = %v (present %v), want %v", seed, present, tt.wantSeed)
			}
		})
	}
}

func TestA1111Txt2ImgUsesTheCheckpointTheClientNames(t *testing.T) {
	p, lane := newA1111TestProxy("qwen-image")
	body := `{"prompt":"x","override_settings":{"sd_model_checkpoint":" dreamshaper8-imagegen-gtx980ti "}}`
	if rec := a1111Do(t, p, http.MethodPost, "/sdapi/v1/txt2img", body, nil); rec.Code != http.StatusOK {
		t.Fatalf("txt2img = %d %s", rec.Code, rec.Body.String())
	}
	if lane.body["model"] != "dreamshaper8-imagegen-gtx980ti" {
		t.Fatalf("model = %v, want the named checkpoint", lane.body["model"])
	}
}

func TestA1111Txt2ImgRefusesBadRequestsBeforeTheLane(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":     `{"prompt":`,
		"no prompt":    `{"width":512}`,
		"blank prompt": `{"prompt":"   "}`,
	} {
		t.Run(name, func(t *testing.T) {
			p, lane := newA1111TestProxy("qwen-image")
			rec := a1111Do(t, p, http.MethodPost, "/sdapi/v1/txt2img", body, nil)
			if rec.Code != http.StatusBadRequest || lane.calls != 0 {
				t.Fatalf("txt2img = %d (lane calls %d), want 400 and no call", rec.Code, lane.calls)
			}
		})
	}
}

func TestA1111Txt2ImgPassesLaneErrorsThrough(t *testing.T) {
	p, lane := newA1111TestProxy("qwen-image")
	lane.reply = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "20")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"model_preempted","code":"model_preempted"}}`)
	}
	rec := a1111Do(t, p, http.MethodPost, "/sdapi/v1/txt2img", `{"prompt":"x"}`, nil)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "20" {
		t.Fatalf("status %d Retry-After %q, want the lane's 503 and 20", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "model_preempted") {
		t.Fatalf("body lost the lane's error: %s", rec.Body.String())
	}
}

func TestA1111Txt2ImgReportsALaneThatReturnsNoImage(t *testing.T) {
	for name, reply := range map[string]string{
		"empty data": `{"created":1,"data":[]}`,
		"not JSON":   `<html>upstream</html>`,
	} {
		t.Run(name, func(t *testing.T) {
			p, lane := newA1111TestProxy("qwen-image")
			lane.reply = func(w http.ResponseWriter) { _, _ = io.WriteString(w, reply) }
			rec := a1111Do(t, p, http.MethodPost, "/sdapi/v1/txt2img", `{"prompt":"x"}`, nil)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("txt2img = %d %s, want 502", rec.Code, rec.Body.String())
			}
		})
	}
}

// The translated request must read as an ordinary image request to the
// proxy's own parser: handleRequest takes the model from the JSON body.
func TestA1111LaneRequestParsesAsAnImageRequest(t *testing.T) {
	p := &Proxy{a1111Model: "qwen-image"}
	body, err := json.Marshal(p.a1111LaneRequest(a1111Txt2ImgRequest{Prompt: "a harbor", Width: 832, Height: 1216}))
	if err != nil {
		t.Fatal(err)
	}
	orig := httptest.NewRequest(http.MethodPost, "/sdapi/v1/txt2img?x=1", strings.NewReader(`{"prompt":"a harbor"}`))
	orig.Header.Set("Authorization", "Bearer t")
	req := a1111LaneHTTPRequest(orig, body)
	if req.URL.Path != "/v1/images/generations" || req.URL.RawQuery != "" || req.Method != http.MethodPost {
		t.Fatalf("request = %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery)
	}
	if req.Header.Get("Authorization") != "Bearer t" {
		t.Errorf("Authorization not carried over")
	}
	model, raw := p.extractModelNameAndBody(req)
	if model != "qwen-image" {
		t.Fatalf("extractModelNameAndBody model = %q, want qwen-image", model)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed["size"] != "832x1216" || parsed["response_format"] != "b64_json" {
		t.Fatalf("parsed body = %v (%v)", parsed, err)
	}
	// The caller's request is untouched.
	if orig.URL.Path != "/sdapi/v1/txt2img" {
		t.Errorf("original request path changed to %s", orig.URL.Path)
	}
}
