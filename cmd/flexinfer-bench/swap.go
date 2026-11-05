package main

// flexinfer-bench swap: the swap-floor profiler (D1 in
// .loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.1
// step 4, kill-test KT-SWAP). It swaps lanes in and out of one runtime pod
// through the runtime API (internal/runtime/api.go), times every swap by the
// load substages the runtime reports in model health
// (internal/runtime/load_progress.go), probes text lanes for decode speed and
// greedy hashes on the three fixed S3 parity prompts, and writes a JSON report
// plus a ready-to-paste .loom/60-validation-matrix.md row.
//
// It talks only to the runtime URL it is given, so the same command measures
// any runtime pod, image or later runtime change. The controller does not
// know about the loads it makes: hold the card (a GPULease on the lane's
// shared group) or use lane names no Model manages, or the controller will
// fight it.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sigs.k8s.io/yaml"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

func init() { registerSubcommand("swap", runSwap) }

// Load substages as the runtime reports them in model health
// (internal/runtime/load_progress.go LoadSubstage*; swap_test.go keeps the two
// in step). swapSubstageSpawn is the time before the backend printed its
// first classified line: process start, interpreter and engine imports.
// swapSubstageReady is the runtime's "backend HTTP server up" substage, so its
// duration is the wait for the model's readiness probe.
const (
	swapSubstageSpawn          = "Spawn"
	swapSubstageLoadingWeights = "LoadingWeights"
	swapSubstageCompiling      = "Compiling"
	swapSubstageProfilingKV    = "ProfilingKV"
	swapSubstageReady          = "Ready"
)

var swapSubstages = []string{
	swapSubstageSpawn,
	swapSubstageLoadingWeights,
	swapSubstageCompiling,
	swapSubstageProfilingKV,
	swapSubstageReady,
}

// swapParityPrompts are the three fixed greedy parity prompts of
// scripts/verify-vllm-mtp.py (R §7b, the S1/S3 probes), verbatim, so a hash
// here compares with the hashes those runs recorded.
var swapParityPrompts = []struct{ Name, Prompt string }{
	{"parity_code", "Write a Python function `merge_intervals(intervals)` that merges " +
		"overlapping closed intervals and returns them sorted. Include a " +
		"docstring and two usage examples with their printed output."},
	{"parity_explain", "Explain, in about 250 words, why TCP uses a three-way handshake " +
		"and what would go wrong with a two-way handshake."},
	{"parity_list", "List eight distinct causes of high p99 latency in a GPU inference " +
		"server, one sentence each, numbered."},
}

// swapDecodePrompt and swapContextFiller build the sized decode prompts, as
// sized_prompt in scripts/verify-vllm-mtp.py does.
const (
	swapDecodePrompt  = "Write a detailed 400-word explanation of how rain forms, from evaporation to rainfall."
	swapContextFiller = "Note %d: sensor %d reported nominal temperature and pressure today.\n"
)

type swapOptions struct {
	RuntimeURL     string
	InferenceURL   string
	ProbeURL       string
	Lanes          []swapLaneFlag
	GPUProfile     string
	Cycles         int
	PollInterval   time.Duration
	LoadTimeout    time.Duration
	RequestTimeout time.Duration
	ParityTokens   int
	DecodeTokens   int
	DecodeContexts []int
	CacheSalt      bool
	Baseline       string
	Out            string
	RowOut         string
	Row            swapRowMeta
	Bars           swapBars
}

type swapLaneFlag struct{ Name, Source string }

// swapBars are the KT-SWAP pass bars the profiler can measure (§6.1); the
// defaults are the pre-registered values.
type swapBars struct {
	TextLoadP50     time.Duration `json:"textLoadP50"`
	TextLoadP95     time.Duration `json:"textLoadP95"`
	OtherLoadP50    time.Duration `json:"otherLoadP50"`
	DecodeTolerance float64       `json:"decodeTolerance"`
}

type swapRowMeta struct {
	ID            string `json:"id"`
	GPUClass      string `json:"gpuClass"`
	RuntimeImage  string `json:"runtimeImage"`
	RollbackImage string `json:"rollbackImage"`
	SpecLink      string `json:"specLink"`
	Command       string `json:"command"`
}

type swapLane struct {
	Name     string   `json:"name"`
	Source   string   `json:"source"`
	Backend  string   `json:"backend"`
	Model    string   `json:"model"`
	SpecHash string   `json:"specHash,omitempty"`
	Text     bool     `json:"text"`
	Context  int64    `json:"maxModelLen,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	payload  json.RawMessage
}

// swapMark is one observed change of a loading model's state or substage,
// in seconds since the runtime accepted the load.
type swapMark struct {
	AtS      float64 `json:"atS"`
	State    string  `json:"state"`
	Substage string  `json:"substage,omitempty"`
	Message  string  `json:"message,omitempty"`
}

type swapProbe struct {
	Name             string   `json:"name"`
	PromptTokens     int      `json:"promptTokens"`
	CompletionTokens int      `json:"completionTokens"`
	TTFTS            float64  `json:"ttftS"`
	DecodeTPS        *float64 `json:"decodeTps"`
	SHA              string   `json:"sha256"`
	Finish           string   `json:"finishReason"`
	Error            string   `json:"error,omitempty"`
}

// swapLoad is one swap-in of a lane: the outgoing lane's unload, the
// admission wait while the runtime still saw its VRAM in use, the load by
// substage, and the probes once Ready.
type swapLoad struct {
	Lane              string             `json:"lane"`
	From              string             `json:"from,omitempty"`
	Cycle             int                `json:"cycle"`
	Warmup            bool               `json:"warmup,omitempty"`
	UnloadS           float64            `json:"unloadS"`
	VRAMUsedMBAfter   *int64             `json:"vramUsedMBAfterUnload,omitempty"`
	AdmitWaitS        float64            `json:"admitWaitS"`
	AdmitRetries      int                `json:"admitRetries,omitempty"`
	LoadS             float64            `json:"loadS"`
	Substages         map[string]float64 `json:"substages"`
	Timeline          []swapMark         `json:"timeline"`
	KVCacheTokens     int64              `json:"kvCacheTokens,omitempty"`
	KVMaxConcurrency  string             `json:"kvMaxConcurrency,omitempty"`
	ServedModel       string             `json:"servedModel,omitempty"`
	FirstTokenS       *float64           `json:"firstTokenS,omitempty"`
	SwapToFirstTokenS *float64           `json:"swapToFirstTokenS,omitempty"`
	Probes            []swapProbe        `json:"probes,omitempty"`
	Error             string             `json:"error,omitempty"`
}

type swapLaneSummary struct {
	Loads          int                 `json:"loads"`
	LoadP50S       float64             `json:"loadP50S"`
	LoadP95S       float64             `json:"loadP95S"`
	UnloadP50S     float64             `json:"unloadP50S"`
	AdmitWaitP50S  float64             `json:"admitWaitP50S"`
	FirstTokenP50S *float64            `json:"firstTokenP50S,omitempty"`
	SubstageP50S   map[string]float64  `json:"substageP50S"`
	Dominant       string              `json:"dominantSubstage,omitempty"`
	DecodeP50TPS   map[string]float64  `json:"decodeP50Tps,omitempty"`
	Hashes         map[string][]string `json:"hashes,omitempty"`
}

type swapBarResult struct {
	Bar    string `json:"bar"`
	Lane   string `json:"lane,omitempty"`
	Rule   string `json:"rule"`
	Value  string `json:"value"`
	Status string `json:"status"` // pass, fail, not_measured
}

type swapReport struct {
	Kind         string                     `json:"kind"`
	StartedAt    time.Time                  `json:"startedAt"`
	RuntimeURL   string                     `json:"runtimeURL,omitempty"`
	ProbeURL     string                     `json:"probeURL,omitempty"`
	Runtime      json.RawMessage            `json:"runtimeStatus,omitempty"`
	OtherModels  []string                   `json:"otherModelsLoaded,omitempty"`
	PollInterval float64                    `json:"pollIntervalS"`
	Lanes        []swapLane                 `json:"lanes,omitempty"`
	Loads        []swapLoad                 `json:"loads,omitempty"`
	Probes       [][]swapProbe              `json:"probeRuns,omitempty"`
	Summary      map[string]swapLaneSummary `json:"summary,omitempty"`
	Bars         []swapBarResult            `json:"bars,omitempty"`
	Verdict      string                     `json:"verdict"`
	Row          swapRowMeta                `json:"row"`
	Error        string                     `json:"error,omitempty"`
}

// swapClock lets tests drive time; the profiler only reads time through it.
type swapClock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realSwapClock struct{}

func (realSwapClock) Now() time.Time { return time.Now() }

func (realSwapClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type swapProfiler struct {
	opts  swapOptions
	http  *http.Client
	clock swapClock
	salt  func() string
}

func runSwap(args []string) int {
	opts, err := parseSwapFlags(args, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "flexinfer-bench swap:", err)
		return 2
	}
	opts.Row.Command = "flexinfer-bench swap " + strings.Join(args, " ")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	p := &swapProfiler{opts: opts, http: &http.Client{}, clock: realSwapClock{}, salt: randomSalt}
	report := p.run(ctx)
	if err := writeSwapOutputs(report, opts); err != nil {
		fmt.Fprintln(os.Stderr, "flexinfer-bench swap:", err)
		return 1
	}
	if report.Error != "" || report.Verdict == "fail" {
		return 1
	}
	return 0
}

func parseSwapFlags(args []string, out io.Writer) (swapOptions, error) {
	fs := flag.NewFlagSet("swap", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() {
		_, _ = fmt.Fprint(out, "Usage: flexinfer-bench swap --runtime-url URL --lane name=source [--lane ...] [flags]\n"+
			"       flexinfer-bench swap --probe-url URL [flags]   (baseline of a Deployment lane)\n\n"+
			"Swaps lanes in and out of one runtime pod through its API and reports the load\n"+
			"substages, decode and greedy hashes per swap (KT-SWAP).\n\n")
		fs.PrintDefaults()
	}
	var opts swapOptions
	var lanes repeatedStringFlag
	var contexts string
	fs.StringVar(&opts.RuntimeURL, "runtime-url", "", "Runtime API base URL (pod IP or Service), e.g. http://10.42.0.137:8080.")
	fs.StringVar(&opts.InferenceURL, "inference-url", "", "OpenAI base URL of a loaded lane; {port} is replaced by the port the runtime reports. Default: the runtime URL's host on that port.")
	fs.StringVar(&opts.ProbeURL, "probe-url", "", "Probe-only mode: run the text probes against this OpenAI base URL (e.g. the Deployment lane's Service) and write a baseline report. No runtime calls.")
	fs.Var(&lanes, "lane", "name=source: a lane to swap in, in order; source is a load payload (.json) or a Model manifest (.yaml, needs --gpuprofile). Repeat; one lane measures reloads.")
	fs.StringVar(&opts.GPUProfile, "gpuprofile", "", "GPUProfile manifest used to render Model-manifest lanes (e.g. deploy/gpuprofiles/gfx1100.yaml).")
	fs.IntVar(&opts.Cycles, "cycles", 5, "Swap-ins per lane after the warmup load (KT-SWAP: 5 each direction). Probe-only: probe runs.")
	fs.DurationVar(&opts.PollInterval, "poll-interval", 250*time.Millisecond, "Model health poll interval; substage durations are accurate to one interval.")
	fs.DurationVar(&opts.LoadTimeout, "load-timeout", 15*time.Minute, "Deadline for one unload or one load to reach Ready.")
	fs.DurationVar(&opts.RequestTimeout, "request-timeout", 10*time.Minute, "Deadline for one probe request.")
	fs.IntVar(&opts.ParityTokens, "parity-tokens", 256, "max_tokens of each greedy parity generation.")
	fs.IntVar(&opts.DecodeTokens, "decode-tokens", 256, "max_tokens of each sized decode probe.")
	fs.StringVar(&contexts, "decode-contexts", "512,2048,16384", "Comma-separated prompt sizes (tokens) for the decode probes (R §7e: 512, 2K, 16K). Empty skips them.")
	fs.BoolVar(&opts.CacheSalt, "cache-salt", true, "Send a fresh vLLM cache_salt with every probe, so each one prefills cold and repeats compare like for like.")
	fs.StringVar(&opts.Baseline, "baseline", "", "A probe-only report of the Deployment lane to compare greedy hashes and decode against.")
	fs.StringVar(&opts.Out, "out", "", "Write the JSON report here (default stdout).")
	fs.StringVar(&opts.RowOut, "row-out", "", "Write the matrix row here (default stderr).")
	fs.StringVar(&opts.Row.ID, "row-id", "gfx1100-xtx-inpod-swap-"+time.Now().UTC().Format("2006-01-02"), "Matrix row id.")
	fs.StringVar(&opts.Row.GPUClass, "gpu-class", "gfx1100/7900xtx", "Matrix row gpu_class.")
	fs.StringVar(&opts.Row.RuntimeImage, "runtime-image", "TBD: runtime digest", "Matrix row runtime_image (the runtime API does not report its image).")
	fs.StringVar(&opts.Row.RollbackImage, "rollback-image", "TBD: rollback digest", "Matrix row rollback_digest.")
	fs.StringVar(&opts.Row.SpecLink, "spec-link", ".loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.1 (KT-SWAP)", "Matrix row spec_roadmap_link.")
	fs.DurationVar(&opts.Bars.TextLoadP50, "max-text-load-p50", 120*time.Second, "KT-SWAP bar: text lane load p50.")
	fs.DurationVar(&opts.Bars.TextLoadP95, "max-text-load-p95", 180*time.Second, "KT-SWAP bar: text lane load p95.")
	fs.DurationVar(&opts.Bars.OtherLoadP50, "max-other-load-p50", 90*time.Second, "KT-SWAP bar: diffusers (non-text) lane load p50.")
	fs.Float64Var(&opts.Bars.DecodeTolerance, "decode-tolerance", 0.03, "KT-SWAP bar: largest decode shortfall against the baseline (0.03 = 3 %).")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected arguments %q", fs.Args())
	}
	for _, raw := range lanes {
		name, source, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(source) == "" {
			return opts, fmt.Errorf("--lane %q: want name=source", raw)
		}
		opts.Lanes = append(opts.Lanes, swapLaneFlag{Name: strings.TrimSpace(name), Source: strings.TrimSpace(source)})
	}
	for _, part := range splitNonEmpty(contexts) {
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return opts, fmt.Errorf("--decode-contexts: %q is not a positive token count", part)
		}
		opts.DecodeContexts = append(opts.DecodeContexts, n)
	}
	switch {
	case opts.ProbeURL != "":
		if len(opts.Lanes) > 0 || opts.RuntimeURL != "" {
			return opts, fmt.Errorf("--probe-url runs without --runtime-url and --lane")
		}
	case opts.RuntimeURL == "" || len(opts.Lanes) == 0:
		return opts, fmt.Errorf("--runtime-url and at least one --lane are required (or --probe-url for a baseline)")
	}
	if opts.Cycles < 1 || opts.PollInterval <= 0 {
		return opts, fmt.Errorf("--cycles must be >= 1 and --poll-interval > 0")
	}
	return opts, nil
}

func randomSalt() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (p *swapProfiler) run(ctx context.Context) *swapReport {
	report := &swapReport{
		Kind:         "swap",
		StartedAt:    p.clock.Now().UTC(),
		RuntimeURL:   p.opts.RuntimeURL,
		PollInterval: p.opts.PollInterval.Seconds(),
		Row:          p.opts.Row,
	}
	if p.opts.ProbeURL != "" {
		report.Kind = "baseline"
		report.ProbeURL = p.opts.ProbeURL
		p.runBaseline(ctx, report)
	} else {
		p.runSwaps(ctx, report)
	}
	p.evaluate(report)
	return report
}

func (p *swapProfiler) runBaseline(ctx context.Context, report *swapReport) {
	served, err := p.servedModel(ctx, p.opts.ProbeURL)
	if err != nil {
		report.Error = err.Error()
		return
	}
	for i := 0; i < p.opts.Cycles; i++ {
		probes, _ := p.probeText(ctx, p.opts.ProbeURL, served)
		report.Probes = append(report.Probes, probes)
		if ctx.Err() != nil {
			report.Error = ctx.Err().Error()
			return
		}
	}
}

func (p *swapProfiler) runSwaps(ctx context.Context, report *swapReport) {
	lanes, err := p.resolveLanes()
	report.Lanes = lanes
	if err != nil {
		report.Error = err.Error()
		return
	}
	if status, err := p.runtimeGet(ctx, "/api/v1/status"); err == nil {
		report.Runtime = status
	}
	known := map[string]bool{}
	for _, l := range lanes {
		known[l.Name] = true
	}
	if loaded, err := p.loadedModels(ctx); err == nil {
		for _, name := range loaded {
			if !known[name] {
				report.OtherModels = append(report.OtherModels, name)
			}
		}
	}
	// Start from an empty slot for every lane: a lane left loaded by an
	// earlier run would make its first swap-in an idempotent no-op.
	for _, l := range lanes {
		if _, err := p.unload(ctx, l.Name); err != nil {
			report.Error = fmt.Sprintf("reset %s: %v", l.Name, err)
			return
		}
	}

	order := []int{0}
	for c := 0; c < p.opts.Cycles; c++ {
		for i := range lanes {
			order = append(order, (i+1)%len(lanes))
		}
	}
	current := ""
	for step, idx := range order {
		lane := lanes[idx]
		cycle := 0
		if step > 0 {
			cycle = (step-1)/len(lanes) + 1
		}
		sample := p.swapIn(ctx, lane, current, cycle)
		sample.Warmup = step == 0
		report.Loads = append(report.Loads, sample)
		if sample.Error != "" {
			report.Error = fmt.Sprintf("%s (cycle %d): %s", lane.Name, cycle, sample.Error)
			return
		}
		current = lane.Name
	}
}

// resolveLanes reads each lane's load payload, rendering Model manifests the
// way the controller does (pkg/runtime.NewLoadPayloadForModel).
func (p *swapProfiler) resolveLanes() ([]swapLane, error) {
	var profile *aiv1alpha2.GPUProfileSpec
	lanes := make([]swapLane, 0, len(p.opts.Lanes))
	for _, lf := range p.opts.Lanes {
		lane := swapLane{Name: lf.Name, Source: lf.Source}
		raw, err := os.ReadFile(lf.Source)
		if err != nil {
			return lanes, fmt.Errorf("lane %s: %w", lf.Name, err)
		}
		var payload pkgrt.LoadPayload
		if strings.HasSuffix(lf.Source, ".json") {
			if err := json.Unmarshal(raw, &payload); err != nil {
				return lanes, fmt.Errorf("lane %s: decode payload: %w", lf.Name, err)
			}
			lane.payload = raw
		} else {
			if profile == nil {
				if profile, err = loadSwapGPUProfile(p.opts.GPUProfile); err != nil {
					return lanes, fmt.Errorf("lane %s: %w", lf.Name, err)
				}
			}
			rendered, warnings, err := renderManifestPayload(raw, profile)
			if err != nil {
				return lanes, fmt.Errorf("lane %s: %w", lf.Name, err)
			}
			payload = *rendered
			lane.Warnings = warnings
			if lane.payload, err = rendered.Marshal(); err != nil {
				return lanes, fmt.Errorf("lane %s: %w", lf.Name, err)
			}
		}
		lane.Backend, lane.Model, lane.SpecHash = payload.Backend, payload.Model, payload.SpecHash
		lane.Text = swapTextBackend(payload.Backend)
		if v, ok := payload.Config["maxModelLen"]; ok {
			if n, err := strconv.ParseInt(fmt.Sprint(v), 10, 64); err == nil {
				lane.Context = n
			}
		}
		if payload.Backend == "" {
			return lanes, fmt.Errorf("lane %s: payload has no backend", lf.Name)
		}
		lanes = append(lanes, lane)
	}
	return lanes, nil
}

func swapTextBackend(name string) bool {
	switch name {
	case backend.NameVLLM, backend.NameLlamaCpp, backend.NameMLCLLM, backend.NameOllama:
		return true
	}
	return false
}

func loadSwapGPUProfile(path string) (*aiv1alpha2.GPUProfileSpec, error) {
	if path == "" {
		return nil, fmt.Errorf("a Model manifest lane needs --gpuprofile")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	gp := &aiv1alpha2.GPUProfile{}
	if err := yaml.UnmarshalStrict(raw, gp); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &gp.Spec, nil
}

// renderManifestPayload renders a Model manifest's runtime load payload and
// names any spec-level reason the controller would keep the Model off the
// runtime path (LoRA adapters are not looked up).
func renderManifestPayload(raw []byte, profile *aiv1alpha2.GPUProfileSpec) (*pkgrt.LoadPayload, []string, error) {
	model := &aiv1alpha2.Model{}
	if err := yaml.UnmarshalStrict(raw, model); err != nil {
		return nil, nil, fmt.Errorf("decode Model manifest: %w", err)
	}
	if model.Kind != "Model" {
		return nil, nil, fmt.Errorf("manifest kind %q, want Model", model.Kind)
	}
	if model.Namespace == "" {
		model.Namespace = "flexinfer-system" // deploy/models/kustomization.yaml
	}
	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		return nil, nil, fmt.Errorf("backend %q is not registered", model.Spec.Backend)
	}
	payload, err := pkgrt.NewLoadPayloadForModel(model, b, pkgrt.BuildLoadOptions{
		ModelBasePath: pkgrt.RuntimeModelBasePath,
		GPUVendor:     pkgrt.ModelGPUVendor(model),
		GPUProfile:    profile,
	})
	if err != nil {
		return nil, nil, err
	}
	// Judge the spec only: a staged cache is the cluster's business.
	staged := model.DeepCopy()
	staged.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
	var warnings []string
	if ok, reason := pkgrt.DirectRuntimeLoadEligibility(staged, b.Name(), profile); !ok {
		warnings = append(warnings, "the controller would not load this Model in-pod: "+reason)
	}
	return payload, warnings, nil
}

// swapIn makes lane the loaded lane: unload from (lane itself when a single
// lane reloads), load lane, probe it.
func (p *swapProfiler) swapIn(ctx context.Context, lane swapLane, from string, cycle int) swapLoad {
	sample := swapLoad{Lane: lane.Name, From: from, Cycle: cycle}
	swapStart := p.clock.Now()
	if from != "" {
		unloadS, err := p.unload(ctx, from)
		sample.UnloadS = round3(unloadS)
		if err != nil {
			sample.Error = fmt.Sprintf("unload %s: %v", from, err)
			return sample
		}
		sample.VRAMUsedMBAfter = p.vramUsedMB(ctx)
	}

	admitStart := p.clock.Now()
	accepted, retries, err := p.requestLoad(ctx, lane)
	sample.AdmitWaitS = round3(accepted.Sub(admitStart).Seconds())
	sample.AdmitRetries = retries
	if err != nil {
		sample.Error = err.Error()
		return sample
	}
	health, marks, readyAt, err := p.waitReady(ctx, lane.Name, accepted)
	sample.Timeline = marks
	sample.Substages = swapSubstageDurations(marks, readyAt.Sub(accepted).Seconds())
	if err != nil {
		sample.Error = err.Error()
		return sample
	}
	sample.LoadS = round3(readyAt.Sub(accepted).Seconds())
	sample.KVCacheTokens, sample.KVMaxConcurrency = health.KVCacheTokens, health.KVMaxConcurrency
	if !lane.Text {
		return sample
	}

	base, err := swapInferenceBase(p.opts.InferenceURL, p.opts.RuntimeURL, health.Port)
	if err != nil {
		sample.Error = err.Error()
		return sample
	}
	if sample.ServedModel, err = p.servedModel(ctx, base); err != nil {
		sample.Error = err.Error()
		return sample
	}
	probes, firstToken := p.probeText(ctx, base, sample.ServedModel)
	sample.Probes = probes
	if len(probes) > 0 && probes[0].Error == "" {
		ttft := probes[0].TTFTS
		total := round3(firstToken.Sub(swapStart).Seconds())
		sample.FirstTokenS, sample.SwapToFirstTokenS = &ttft, &total
	}
	for _, pr := range probes {
		if pr.Error != "" {
			sample.Error = fmt.Sprintf("probe %s: %s", pr.Name, pr.Error)
			break
		}
	}
	return sample
}

// unload removes name from the runtime, waiting out request drain (409).
// A model that is not loaded counts as unloaded at once.
func (p *swapProfiler) unload(ctx context.Context, name string) (float64, error) {
	start := p.clock.Now()
	deadline := start.Add(p.opts.LoadTimeout)
	for {
		code, body, err := p.runtimeDo(ctx, http.MethodDelete, "/api/v1/models/"+url.PathEscape(name), nil)
		switch {
		case err != nil:
			return 0, err
		case code == http.StatusOK || code == http.StatusNotFound:
			return p.clock.Now().Sub(start).Seconds(), nil
		case code != http.StatusConflict:
			return 0, fmt.Errorf("DELETE returned HTTP %d: %s", code, swapErrorText(body))
		}
		if p.clock.Now().After(deadline) {
			return 0, fmt.Errorf("still draining after %v", p.opts.LoadTimeout)
		}
		if err := p.clock.Sleep(ctx, p.opts.PollInterval); err != nil {
			return 0, err
		}
	}
}

// requestLoad POSTs the lane's payload. 503 (the runtime still counts the
// outgoing lane's VRAM or compute queues) and 409 (a same-name instance is
// draining) are retried; the time they take is the admission wait.
func (p *swapProfiler) requestLoad(ctx context.Context, lane swapLane) (time.Time, int, error) {
	deadline := p.clock.Now().Add(p.opts.LoadTimeout)
	retries := 0
	for {
		code, body, err := p.runtimeDo(ctx, http.MethodPost, "/api/v1/models/"+url.PathEscape(lane.Name)+"/load", lane.payload)
		now := p.clock.Now()
		switch {
		case err != nil:
			return now, retries, fmt.Errorf("load: %w", err)
		case code == http.StatusAccepted || code == http.StatusOK:
			return now, retries, nil
		case code != http.StatusServiceUnavailable && code != http.StatusConflict:
			return now, retries, fmt.Errorf("load returned HTTP %d: %s", code, swapErrorText(body))
		}
		if now.After(deadline) {
			return now, retries, fmt.Errorf("load not admitted after %v: %s", p.opts.LoadTimeout, swapErrorText(body))
		}
		retries++
		if err := p.clock.Sleep(ctx, p.opts.PollInterval); err != nil {
			return now, retries, err
		}
	}
}

type swapHealth struct {
	State            string `json:"state"`
	Error            string `json:"error"`
	Port             int32  `json:"port"`
	Draining         bool   `json:"draining"`
	LoadingSubstage  string `json:"loadingSubstage"`
	LoadingMessage   string `json:"loadingMessage"`
	KVCacheTokens    int64  `json:"kvCacheTokens"`
	KVMaxConcurrency string `json:"kvMaxConcurrency"`
}

// waitReady polls model health until the model is Ready, recording every
// change of state or substage.
func (p *swapProfiler) waitReady(ctx context.Context, name string, accepted time.Time) (swapHealth, []swapMark, time.Time, error) {
	deadline := accepted.Add(p.opts.LoadTimeout)
	var marks []swapMark
	for {
		code, body, err := p.runtimeDo(ctx, http.MethodGet, "/api/v1/models/"+url.PathEscape(name)+"/health", nil)
		now := p.clock.Now()
		if err != nil {
			return swapHealth{}, marks, now, err
		}
		if code == http.StatusNotFound {
			return swapHealth{}, marks, now, fmt.Errorf("model left the runtime while loading: %s", swapErrorText(body))
		}
		var h swapHealth
		if err := json.Unmarshal(body, &h); err != nil {
			return h, marks, now, fmt.Errorf("decode health (HTTP %d): %w", code, err)
		}
		substage := h.LoadingSubstage
		if h.State == "Loading" && substage == "" {
			substage = swapSubstageSpawn
		}
		if h.State != "Loading" {
			substage = ""
		}
		if n := len(marks); n == 0 || marks[n-1].State != h.State || marks[n-1].Substage != substage {
			marks = append(marks, swapMark{AtS: round3(now.Sub(accepted).Seconds()), State: h.State, Substage: substage, Message: h.LoadingMessage})
		}
		switch h.State {
		case "Ready":
			if !h.Draining {
				return h, marks, now, nil
			}
		case "Failed", "Stopping":
			return h, marks, now, fmt.Errorf("model %s: %s", strings.ToLower(h.State), h.Error)
		}
		if now.After(deadline) {
			return h, marks, now, fmt.Errorf("not Ready after %v (state %s, substage %s)", p.opts.LoadTimeout, h.State, substage)
		}
		if err := p.clock.Sleep(ctx, p.opts.PollInterval); err != nil {
			return h, marks, now, err
		}
	}
}

// swapSubstageDurations sums the time spent in each substage. A mark holds
// until the next one; vLLM visits Compiling both before and after the KV
// profile, so a substage can be entered more than once. totalS closes the
// last Loading mark.
func swapSubstageDurations(marks []swapMark, totalS float64) map[string]float64 {
	out := map[string]float64{}
	for i, m := range marks {
		if m.State != "Loading" {
			continue
		}
		end := totalS
		if i+1 < len(marks) {
			end = marks[i+1].AtS
		}
		out[m.Substage] = round3(out[m.Substage] + end - m.AtS)
	}
	return out
}

func (p *swapProfiler) vramUsedMB(ctx context.Context) *int64 {
	body, err := p.runtimeGet(ctx, "/api/v1/status")
	if err != nil {
		return nil
	}
	var status struct {
		GPU struct {
			VRAMUsedMB int64 `json:"vramUsedMB"`
		} `json:"gpu"`
	}
	if json.Unmarshal(body, &status) != nil {
		return nil
	}
	return &status.GPU.VRAMUsedMB
}

func (p *swapProfiler) loadedModels(ctx context.Context) ([]string, error) {
	body, err := p.runtimeGet(ctx, "/api/v1/models")
	if err != nil {
		return nil, err
	}
	var list struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Models))
	for _, m := range list.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

func (p *swapProfiler) runtimeGet(ctx context.Context, path string) ([]byte, error) {
	code, body, err := p.runtimeDo(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned HTTP %d", path, code)
	}
	return body, nil
}

func (p *swapProfiler) runtimeDo(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, strings.TrimRight(p.opts.RuntimeURL, "/")+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, data, err
}

func swapErrorText(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(body))
}

// swapInferenceBase is the OpenAI base URL of a lane the runtime serves on
// port: the template with {port} replaced, or the runtime URL's host on that
// port.
func swapInferenceBase(template, runtimeURL string, port int32) (string, error) {
	if template != "" {
		if strings.Contains(template, "{port}") {
			if port <= 0 {
				return "", fmt.Errorf("--inference-url uses {port} but the runtime reported none")
			}
			template = strings.ReplaceAll(template, "{port}", strconv.Itoa(int(port)))
		}
		return strings.TrimRight(template, "/"), nil
	}
	if port <= 0 {
		return "", fmt.Errorf("the runtime reported no model port; pass --inference-url")
	}
	u, err := url.Parse(runtimeURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("cannot derive the inference URL from --runtime-url %q", runtimeURL)
	}
	u.Host = fmt.Sprintf("%s:%d", u.Hostname(), port)
	u.Path = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// servedModel returns the first model id the OpenAI server lists.
func (p *swapProfiler) servedModel(ctx context.Context, base string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return "", err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("list served models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil || len(list.Data) == 0 {
		return "", fmt.Errorf("list served models: HTTP %d, no model id", resp.StatusCode)
	}
	return list.Data[0].ID, nil
}

// probeText runs the three parity generations, then one decode probe per
// context size. It returns when a probe fails, with that probe last, and the
// time the first one produced its first token.
func (p *swapProfiler) probeText(ctx context.Context, base, model string) ([]swapProbe, time.Time) {
	var probes []swapProbe
	var firstToken time.Time
	for i, pp := range swapParityPrompts {
		pr, first := p.chat(ctx, base, model, pp.Name, pp.Prompt, p.opts.ParityTokens)
		if i == 0 {
			firstToken = first
		}
		probes = append(probes, pr)
		if pr.Error != "" {
			return probes, firstToken
		}
	}
	for _, target := range p.opts.DecodeContexts {
		name := fmt.Sprintf("decode@%d", target)
		prompt, err := p.sizedPrompt(ctx, base, model, target)
		if err != nil {
			return append(probes, swapProbe{Name: name, Error: err.Error()}), firstToken
		}
		pr, _ := p.chat(ctx, base, model, name, prompt, p.opts.DecodeTokens)
		probes = append(probes, pr)
		if pr.Error != "" {
			return probes, firstToken
		}
	}
	return probes, firstToken
}

// sizedPrompt builds a prompt of target ±10 % tokens that opens with a unique
// line, so no two probes share a cached prefix.
func (p *swapProfiler) sizedPrompt(ctx context.Context, base, model string, target int) (string, error) {
	low, high := float64(target)*0.9, float64(target)*1.1
	head := "Session " + p.salt() + ".\n"
	repeats := max(1, target/16)
	for range 8 {
		var sb strings.Builder
		sb.WriteString(head)
		sb.WriteString("Context log:\n")
		for i := 0; i < repeats; i++ {
			fmt.Fprintf(&sb, swapContextFiller, i, i)
		}
		sb.WriteString("\nIgnore the log above. ")
		sb.WriteString(swapDecodePrompt)
		prompt := sb.String()
		count, err := p.tokenize(ctx, base, model, prompt)
		if err != nil {
			return "", err
		}
		if c := float64(count); c >= low && c <= high {
			return prompt, nil
		}
		repeats = max(1, int(math.Round(float64(repeats)*float64(target)/float64(max(count, 1)))))
	}
	return "", fmt.Errorf("could not size a ~%d-token prompt", target)
}

func (p *swapProfiler) tokenize(ctx context.Context, base, model, prompt string) (int, error) {
	body, _ := json.Marshal(map[string]any{
		"model":                 model,
		"add_generation_prompt": true,
		"messages":              []map[string]string{{"role": "user", "content": prompt}},
	})
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/tokenize", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("tokenize: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("tokenize: HTTP %d", resp.StatusCode)
	}
	return out.Count, nil
}

// chat runs one greedy streaming chat completion and measures it as
// scripts/verify-vllm-mtp.py does: TTFT to the first content token, decode
// as (tokens-1) over first-to-last token, sha256 of the stripped answer.
func (p *swapProfiler) chat(ctx context.Context, base, model, name, prompt string, maxTokens int) (swapProbe, time.Time) {
	pr := swapProbe{Name: name}
	req := map[string]any{
		"model":          model,
		"messages":       []map[string]string{{"role": "user", "content": prompt}},
		"temperature":    0,
		"max_tokens":     maxTokens,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if p.opts.CacheSalt {
		req["cache_salt"] = p.salt()
	}
	body, _ := json.Marshal(req)
	reqCtx, cancel := context.WithTimeout(ctx, p.opts.RequestTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		pr.Error = err.Error()
		return pr, time.Time{}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	started := p.clock.Now()
	resp, err := p.http.Do(httpReq)
	if err != nil {
		pr.Error = err.Error()
		return pr, time.Time{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		pr.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, swapErrorText(data))
		return pr, time.Time{}
	}
	return readSwapChatStream(resp.Body, name, started, p.clock.Now)
}

func readSwapChatStream(r io.Reader, name string, started time.Time, now func() time.Time) (swapProbe, time.Time) {
	pr := swapProbe{Name: name}
	var content strings.Builder
	var first, last time.Time
	var usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	}
	done := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			done = true
			break
		}
		var event struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			pr.Error = "bad SSE event: " + err.Error()
			return pr, first
		}
		if len(event.Error) > 0 && string(event.Error) != "null" {
			pr.Error = "stream error: " + string(event.Error)
			return pr, first
		}
		if event.Usage != nil {
			usage = event.Usage
		}
		for _, c := range event.Choices {
			text := c.Delta.Content + c.Delta.ReasoningContent + c.Delta.Reasoning
			if text != "" {
				last = now()
				if first.IsZero() {
					first = last
				}
				content.WriteString(c.Delta.Content)
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				pr.Finish = *c.FinishReason
			}
		}
	}
	switch {
	case sc.Err() != nil:
		pr.Error = sc.Err().Error()
	case !done || first.IsZero() || pr.Finish == "":
		pr.Error = "incomplete or empty SSE response"
	case usage == nil || usage.CompletionTokens <= 0:
		pr.Error = "missing completion-token usage"
	}
	if pr.Error != "" {
		return pr, first
	}
	answer := strings.TrimSpace(content.String())
	sum := sha256.Sum256([]byte(answer))
	pr.SHA = hex.EncodeToString(sum[:])[:16]
	pr.PromptTokens, pr.CompletionTokens = usage.PromptTokens, usage.CompletionTokens
	pr.TTFTS = round3(first.Sub(started).Seconds())
	if span := last.Sub(first).Seconds(); span > 0 {
		tps := round3(float64(usage.CompletionTokens-1) / span)
		pr.DecodeTPS = &tps
	}
	return pr, first
}

// evaluate fills the per-lane summary, the KT-SWAP bars and the verdict.
func (p *swapProfiler) evaluate(report *swapReport) {
	report.Summary = map[string]swapLaneSummary{}
	switch report.Kind {
	case "baseline":
		report.Summary["baseline"] = summarizeProbes(swapLaneSummary{}, report.Probes)
		report.Verdict = "baseline"
		if report.Error != "" {
			report.Verdict = "fail"
		}
		return
	}
	var baseline *swapReport
	if p.opts.Baseline != "" {
		b, err := readSwapReport(p.opts.Baseline)
		if err != nil {
			report.Error = strings.TrimSpace(report.Error + "; baseline: " + err.Error())
		} else {
			baseline = b
		}
	}
	for _, lane := range report.Lanes {
		s := summarizeLane(lane, report.Loads)
		report.Summary[lane.Name] = s
		report.Bars = append(report.Bars, p.laneBars(lane, s, baseline)...)
	}
	report.Bars = append(report.Bars,
		swapBarResult{Bar: "smu_amdgpu_errors", Rule: "0 SMU/amdgpu errors in the host log", Value: "read the host kernel log for the run window", Status: "not_measured"},
		swapBarResult{Bar: "warm_lane_return", Rule: "warm lane back within the 5-min swap cooldown after each render", Value: "controller-driven; read Model status after the run", Status: "not_measured"},
	)
	report.Verdict = swapVerdict(report)
}

func summarizeLane(lane swapLane, loads []swapLoad) swapLaneSummary {
	s := swapLaneSummary{SubstageP50S: map[string]float64{}}
	var loadS, unloadS, admitS, firstS []float64
	substages := map[string][]float64{}
	var probeRuns [][]swapProbe
	for _, l := range loads {
		if l.Lane != lane.Name || l.Error != "" {
			continue
		}
		if len(l.Probes) > 0 {
			probeRuns = append(probeRuns, l.Probes)
		}
		if l.Warmup {
			continue
		}
		loadS = append(loadS, l.LoadS)
		unloadS = append(unloadS, l.UnloadS)
		admitS = append(admitS, l.AdmitWaitS)
		if l.FirstTokenS != nil {
			firstS = append(firstS, *l.FirstTokenS)
		}
		for _, name := range swapSubstages {
			substages[name] = append(substages[name], l.Substages[name])
		}
	}
	s.Loads = len(loadS)
	s.LoadP50S, s.LoadP95S = percentile(loadS, 50), percentile(loadS, 95)
	s.UnloadP50S, s.AdmitWaitP50S = percentile(unloadS, 50), percentile(admitS, 50)
	if len(firstS) > 0 {
		v := percentile(firstS, 50)
		s.FirstTokenP50S = &v
	}
	best := 0.0
	for _, name := range swapSubstages {
		v := percentile(substages[name], 50)
		s.SubstageP50S[name] = v
		if v > best {
			best, s.Dominant = v, name
		}
	}
	return summarizeProbes(s, probeRuns)
}

// summarizeProbes adds the median decode per probe and every distinct hash
// per parity prompt, over all probe runs.
func summarizeProbes(s swapLaneSummary, runs [][]swapProbe) swapLaneSummary {
	decode := map[string][]float64{}
	hashes := map[string][]string{}
	for _, run := range runs {
		for _, pr := range run {
			if pr.Error != "" {
				continue
			}
			if pr.DecodeTPS != nil {
				decode[pr.Name] = append(decode[pr.Name], *pr.DecodeTPS)
			}
			if strings.HasPrefix(pr.Name, "parity_") && !slices.Contains(hashes[pr.Name], pr.SHA) {
				hashes[pr.Name] = append(hashes[pr.Name], pr.SHA)
			}
		}
	}
	if len(decode) > 0 {
		s.DecodeP50TPS = map[string]float64{}
		for name, v := range decode {
			s.DecodeP50TPS[name] = percentile(v, 50)
		}
	}
	if len(hashes) > 0 {
		s.Hashes = hashes
	}
	return s
}

func (p *swapProfiler) laneBars(lane swapLane, s swapLaneSummary, baseline *swapReport) []swapBarResult {
	var bars []swapBarResult
	status := func(ok bool) string {
		if s.Loads == 0 {
			return "not_measured"
		}
		if ok {
			return "pass"
		}
		return "fail"
	}
	if lane.Text {
		bars = append(bars,
			swapBarResult{Bar: "load_p50", Lane: lane.Name, Rule: "<= " + p.opts.Bars.TextLoadP50.String(), Value: fmt.Sprintf("%.1fs over %d loads", s.LoadP50S, s.Loads), Status: status(s.LoadP50S <= p.opts.Bars.TextLoadP50.Seconds())},
			swapBarResult{Bar: "load_p95", Lane: lane.Name, Rule: "<= " + p.opts.Bars.TextLoadP95.String(), Value: fmt.Sprintf("%.1fs over %d loads", s.LoadP95S, s.Loads), Status: status(s.LoadP95S <= p.opts.Bars.TextLoadP95.Seconds())},
		)
	} else {
		bars = append(bars, swapBarResult{Bar: "load_p50", Lane: lane.Name, Rule: "<= " + p.opts.Bars.OtherLoadP50.String(), Value: fmt.Sprintf("%.1fs over %d loads", s.LoadP50S, s.Loads), Status: status(s.LoadP50S <= p.opts.Bars.OtherLoadP50.Seconds())})
		return bars
	}
	parity := swapBarResult{Bar: "greedy_hash", Lane: lane.Name, Rule: "identical to the Deployment lane on the three S3 prompts"}
	decode := swapBarResult{Bar: "decode", Lane: lane.Name, Rule: fmt.Sprintf("median within %.0f %% of the Deployment lane", p.opts.Bars.DecodeTolerance*100)}
	if baseline == nil {
		parity.Status, parity.Value = "not_measured", "no --baseline; in-pod hashes "+formatHashes(s.Hashes)
		decode.Status, decode.Value = "not_measured", "no --baseline"
		return append(bars, parity, decode)
	}
	base := baseline.Summary["baseline"]
	parity.Status, parity.Value = "pass", formatHashes(s.Hashes)+" vs "+formatHashes(base.Hashes)
	for _, pp := range swapParityPrompts {
		got, want := s.Hashes[pp.Name], base.Hashes[pp.Name]
		if len(got) != 1 || len(want) != 1 || got[0] != want[0] {
			parity.Status = "fail"
		}
	}
	decode.Status = "pass"
	var parts []string
	names := make([]string, 0, len(base.DecodeP50TPS))
	for name := range base.DecodeP50TPS {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := base.DecodeP50TPS[name]
		got, ok := s.DecodeP50TPS[name]
		if !ok || want <= 0 {
			decode.Status = "fail"
			parts = append(parts, name+" missing")
			continue
		}
		ratio := got / want
		parts = append(parts, fmt.Sprintf("%s %.1f/%.1f (%.3f)", name, got, want, ratio))
		if ratio < 1-p.opts.Bars.DecodeTolerance {
			decode.Status = "fail"
		}
	}
	if len(names) == 0 {
		decode.Status = "not_measured"
	}
	decode.Value = strings.Join(parts, ", ")
	return append(bars, parity, decode)
}

// swapVerdict: fail on any failed bar or run error; pass when every bar
// passed; conditional when only bars outside the profiler are open.
func swapVerdict(report *swapReport) string {
	if report.Error != "" {
		return "fail"
	}
	verdict := "pass"
	for _, b := range report.Bars {
		switch b.Status {
		case "fail":
			return "fail"
		case "not_measured":
			verdict = "conditional"
		}
	}
	return verdict
}

func formatHashes(h map[string][]string) string {
	if len(h) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(swapParityPrompts))
	for _, pp := range swapParityPrompts {
		parts = append(parts, pp.Name+"="+strings.Join(h[pp.Name], "|"))
	}
	return strings.Join(parts, " ")
}

func readSwapReport(path string) (*swapReport, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r swapReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.Kind != "baseline" {
		return nil, fmt.Errorf("%s is a %q report, want a --probe-url baseline", path, r.Kind)
	}
	return &r, nil
}

func writeSwapOutputs(report *swapReport, opts swapOptions) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if opts.Out == "" {
		_, err = os.Stdout.Write(data)
	} else {
		err = os.WriteFile(opts.Out, data, 0o644)
	}
	if err != nil {
		return err
	}
	row := swapMatrixRow(report) + "\n"
	if opts.RowOut == "" {
		_, err = os.Stderr.WriteString(row)
		return err
	}
	return os.WriteFile(opts.RowOut, []byte(row), 0o644)
}

// swapMatrixRow renders the report as one row of the .loom/60-validation-
// matrix.md promotion table (13 columns).
func swapMatrixRow(r *swapReport) string {
	var backends, sources, contexts []string
	for _, l := range r.Lanes {
		backends = append(backends, fmt.Sprintf("`%s` (%s)", l.Backend, l.Name))
		sources = append(sources, fmt.Sprintf("`%s`", l.Model))
		if l.Context > 0 {
			contexts = append(contexts, fmt.Sprintf("%d (%s)", l.Context, l.Name))
		}
	}
	if len(contexts) == 0 {
		contexts = []string{"n/a"}
	}
	var evidence []string
	evidence = append(evidence, fmt.Sprintf("**%s %s** (`flexinfer-bench swap`, %s, poll %.2fs).", strings.ToUpper(r.Verdict), r.StartedAt.Format("2006-01-02 15:04Z"), r.Kind, r.PollInterval))
	for _, l := range r.Lanes {
		s, ok := r.Summary[l.Name]
		if !ok {
			continue
		}
		var subs []string
		for _, name := range swapSubstages {
			subs = append(subs, fmt.Sprintf("%s %.1f", name, s.SubstageP50S[name]))
		}
		line := fmt.Sprintf("%s: %d loads, load p50 %.1fs / p95 %.1fs, unload p50 %.1fs, admission wait p50 %.1fs; substage p50 s: %s (dominant %s)",
			l.Name, s.Loads, s.LoadP50S, s.LoadP95S, s.UnloadP50S, s.AdmitWaitP50S, strings.Join(subs, ", "), s.Dominant)
		if s.FirstTokenP50S != nil {
			line += fmt.Sprintf("; first token after Ready p50 %.2fs", *s.FirstTokenP50S)
		}
		evidence = append(evidence, line+".")
	}
	var bars []string
	for _, b := range r.Bars {
		lane := ""
		if b.Lane != "" {
			lane = b.Lane + " "
		}
		bars = append(bars, fmt.Sprintf("%s%s %s: %s (%s)", lane, b.Bar, b.Rule, b.Value, b.Status))
	}
	if len(bars) > 0 {
		evidence = append(evidence, "Bars: "+strings.Join(bars, "; ")+".")
	}
	failure := "none"
	if r.Error != "" {
		failure = r.Error
	}
	decision := map[string]string{"pass": "promote", "conditional": "conditional", "fail": "fail"}[r.Verdict]
	if decision == "" {
		decision = "pending"
	}
	cells := []string{
		fmt.Sprintf("`%s` — KT-SWAP in-pod swap floor", r.Row.ID),
		strings.Join(contexts, ", "),
		"`" + r.Row.GPUClass + "`",
		strings.Join(backends, ", "),
		"`experimental`",
		r.Row.RuntimeImage,
		strings.Join(sources, ", "),
		strings.Join(evidence, " "),
		failure,
		"`" + r.Row.Command + "`",
		r.Row.RollbackImage,
		r.Row.SpecLink,
		"`" + decision + "`",
	}
	for i, c := range cells {
		cells[i] = strings.ReplaceAll(c, "|", "\\|")
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

// percentile is the nearest-rank percentile (p95 of five samples is the max).
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = min(max(rank, 1), len(sorted))
	return round3(sorted[rank-1])
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
