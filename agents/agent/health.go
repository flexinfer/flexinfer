package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// GPU health sentinel.
//
// Why this exists: between 2026-10-04 and 2026-10-22 the RX 7900 XTX in
// cblevins-7900xtx dropped off the PCIe bus under sustained load six times.
// Each time the signature was the same — hwmon/sysfs reads failed with
// "Remote I/O error", current_link_speed read "Unknown", and rocm-smi / rocminfo
// went to state D — yet nothing in the control plane noticed: the agent itself
// hung inside rocm-smi (no per-command timeout), the node stayed Ready, the
// scheduler kept placing the warm lane on the card, and every activation ran
// its full 25-minute cold-start timeout before the proxy gave up. The operator
// parked the lane by hand with a bogus hostname selector after each drop.
//
// The sentinel turns those signatures into a node label the scheduler
// extender and the controller honor (flexinfer.ai/gpu.health=degraded), so a
// dead card is parked automatically, the proxy fast-fails requests for lanes
// pinned to it, and an alert fires. It is deliberately conservative: only
// signals that have never been observed on a healthy, awake card mark a GPU
// degraded, and a runtime-suspended card (power/runtime_status=suspended,
// where the PCIe config space is legitimately unreadable) is left alone.

// Health reasons published on the node annotation and the degraded counter.
const (
	// HealthReasonPCIeLinkDown: the discrete card's current_link_speed reads
	// "Unknown" while the device is awake. amdgpu reports that only when the
	// PCIe capability cannot be read, i.e. the card is off the bus.
	HealthReasonPCIeLinkDown = "pcie-link-down"
	// HealthReasonDeviceIOError: a sysfs read that goes through the SMU or the
	// driver (gpu_busy_percent, hwmon temperature/power, link state) failed
	// with EIO/EREMOTEIO/ENXIO. The firmware stopped answering.
	HealthReasonDeviceIOError = "device-io-error"
	// HealthReasonProbeTimeout: a vendor tool (rocm-smi, rocminfo, nvidia-smi)
	// did not return within the probe timeout. On a wedged card those tools
	// sit in uninterruptible sleep.
	HealthReasonProbeTimeout = "probe-timeout"
	// HealthReasonGPUMissing: a discrete card that this agent process had
	// enumerated before is no longer in sysfs.
	HealthReasonGPUMissing = "gpu-missing"
	// HealthReasonFallenOffBus: nvidia-smi reported "GPU has fallen off the bus".
	HealthReasonFallenOffBus = "fallen-off-bus"
)

const (
	// defaultProbeTimeout bounds every vendor-tool invocation. 15 s is far above
	// the ~1 s a healthy rocm-smi/nvidia-smi takes and well below the 30 s
	// probe interval, so a hung tool costs one cycle, not the agent.
	defaultProbeTimeout = 15 * time.Second
	// defaultProbeWaitDelay is how long exec waits for a killed child to exit
	// before abandoning it. A child in state D ignores SIGKILL until the
	// driver releases it; without this bound Wait() would block forever.
	defaultProbeWaitDelay = 2 * time.Second
	// healthRecoveryStreak is the number of consecutive clean cycles needed to
	// clear a degraded verdict. Flips to degraded are immediate (the signals
	// are unambiguous); recovery is damped so a card that is half-answering
	// does not flap the scheduler.
	healthRecoveryStreak = 3
)

// errProbeTimeout wraps vendor-tool timeouts so callers can tell a hung tool
// from a missing one.
var errProbeTimeout = errors.New("gpu probe timed out")

// errProbeSkipped is returned by exec when the sysfs sentinel has already
// found the card unresponsive this cycle and running vendor tools against it
// would only create more processes stuck in state D.
var errProbeSkipped = errors.New("gpu probe skipped: device degraded")

// GPUHealth is the per-card verdict of one probe cycle.
type GPUHealth struct {
	Index   int
	Vendor  string
	Healthy bool
	Reason  string // one of the HealthReason* constants; empty when healthy
	Detail  string // human-readable evidence, e.g. "current_link_speed=Unknown"
}

// String renders the verdict the way it is published on the node annotation.
func (h GPUHealth) String() string {
	if h.Healthy {
		return fmt.Sprintf("card%d: ok", h.Index)
	}
	if h.Detail == "" {
		return fmt.Sprintf("card%d: %s", h.Index, h.Reason)
	}
	return fmt.Sprintf("card%d: %s (%s)", h.Index, h.Reason, h.Detail)
}

// NodeHealth is the node-level verdict derived from the per-card ones.
type NodeHealth struct {
	// Known is false when the agent has no GPU evidence at all (no sysfs
	// cards, no tool output, no previous enumeration). Nothing is published.
	Known    bool
	Degraded bool
	Reason   string    // joined per-card reasons while degraded; empty otherwise
	Since    time.Time // last transition
	Cards    []GPUHealth
}

// healthState is the sentinel's memory across probe cycles.
type healthState struct {
	mu sync.Mutex

	// Per-cycle evidence recorded by exec.
	cycleTimeouts    map[string]string // tool name -> detail
	cycleToolVerdict map[string]string // reason -> detail (e.g. fallen-off-bus)
	skipTools        bool              // sysfs found the card dead; do not exec vendor tools

	// Cross-cycle memory.
	lastDiscreteCount int // -1 until the first enumeration
	degraded          bool
	reason            string
	since             time.Time
	cleanStreak       int
	known             bool
	lastCards         map[string]GPUHealth // "vendor/index" -> last verdict, for metrics cleanup
}

func newHealthState() *healthState {
	return &healthState{
		cycleTimeouts:     map[string]string{},
		cycleToolVerdict:  map[string]string{},
		lastDiscreteCount: -1,
		lastCards:         map[string]GPUHealth{},
	}
}

// beginCycle clears the per-cycle evidence.
func (s *healthState) beginCycle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cycleTimeouts = map[string]string{}
	s.cycleToolVerdict = map[string]string{}
	s.skipTools = false
}

func (s *healthState) noteTimeout(tool, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cycleTimeouts[tool]; !ok {
		s.cycleTimeouts[tool] = detail
	}
}

func (s *healthState) noteToolVerdict(reason, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cycleToolVerdict[reason]; !ok {
		s.cycleToolVerdict[reason] = detail
	}
}

func (s *healthState) shouldSkipTools() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipTools
}

// exec runs a vendor tool with the probe timeout and records health evidence
// from the outcome. Every hardware probe in the agent goes through here so a
// hung rocm-smi or nvidia-smi can neither stall the agent nor go unnoticed.
func (a *Agent) exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	tool := probeToolName(name, args)
	if a.health != nil && a.healthSentinel && isGPUTool(tool) && a.health.shouldSkipTools() {
		return nil, errProbeSkipped
	}

	timeout := a.probeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, err := a.runCmd(cctx, name, args...)
	if err != nil && errors.Is(cctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		err = fmt.Errorf("%s did not return within %s: %w", tool, timeout, errProbeTimeout)
		if a.health != nil && isGPUTool(tool) {
			a.health.noteTimeout(tool, err.Error())
		}
		return out, err
	}
	if a.health != nil && tool == "nvidia-smi" && len(out) > 0 {
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "fallen off the bus") {
			a.health.noteToolVerdict(HealthReasonFallenOffBus, "nvidia-smi: GPU has fallen off the bus")
		}
	}
	return out, err
}

// probeToolName collapses "chroot /host rocm-smi ..." to "rocm-smi".
func probeToolName(name string, args []string) string {
	base := filepath.Base(name)
	if base == "chroot" && len(args) >= 2 {
		return filepath.Base(args[1])
	}
	return base
}

func isGPUTool(tool string) bool {
	switch tool {
	case "rocm-smi", "rocminfo", "nvidia-smi", "amd-smi":
		return true
	}
	return false
}

// readSysfsFile is os.ReadFile behind a variable so tests can inject the
// EIO/EREMOTEIO failures a wedged device produces (a tmpfs fixture cannot).
var readSysfsFile = os.ReadFile

// parseBytes parses a byte count string (sysfs mem_info_vram_* files).
func parseBytes(s string) uint64 {
	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return val
}

// amdSysfsCard is one amdgpu device as enumerated from sysfs.
type amdSysfsCard struct {
	index      int
	devicePath string
	totalMB    uint64
}

// enumerateAMDSysfsCards lists amdgpu devices with their VRAM totals.
func (a *Agent) enumerateAMDSysfsCards() []amdSysfsCard {
	root := a.sysfsRoot
	if root == "" {
		root = "/sys"
	}
	cardPaths, err := filepath.Glob(filepath.Join(root, "class/drm/card[0-9]*"))
	if err != nil {
		return nil
	}
	var cards []amdSysfsCard
	for _, cardPath := range cardPaths {
		cardName := filepath.Base(cardPath)
		if strings.Contains(cardName, "-") {
			continue // connector entries such as card0-DP-1
		}
		devicePath := filepath.Join(cardPath, "device")
		data, err := readSysfsFile(filepath.Join(devicePath, "mem_info_vram_total"))
		if err != nil {
			continue // not amdgpu
		}
		idx, err := strconv.Atoi(strings.TrimPrefix(cardName, "card"))
		if err != nil {
			continue
		}
		cards = append(cards, amdSysfsCard{
			index:      idx,
			devicePath: devicePath,
			totalMB:    parseBytes(strings.TrimSpace(string(data))) / (1024 * 1024),
		})
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].index < cards[j].index })
	return cards
}

// discreteAMDSysfsCards drops iGPU entries (the Raphael iGPU on cblevins-7900xtx
// shows up as a 512 MB amdgpu device) using the same VRAM-ratio heuristic as
// the label and metrics paths.
func (a *Agent) discreteAMDSysfsCards() []amdSysfsCard {
	cards := a.enumerateAMDSysfsCards()
	if len(cards) == 0 {
		return nil
	}
	totals := make([]uint64, 0, len(cards))
	for _, c := range cards {
		totals = append(totals, c.totalMB)
	}
	cutoff, _, _ := filterAMDDiscreteTotals(totals)
	if cutoff == 0 {
		return cards
	}
	out := make([]amdSysfsCard, 0, len(cards))
	for _, c := range cards {
		if c.totalMB >= cutoff {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return cards
	}
	return out
}

// probeAMDHealthSysfs inspects every discrete amdgpu device through sysfs.
// It never executes anything, so it is safe to run first in a cycle even when
// the card has taken the vendor tools down with it.
func (a *Agent) probeAMDHealthSysfs() []GPUHealth {
	cards := a.discreteAMDSysfsCards()
	verdicts := make([]GPUHealth, 0, len(cards))
	for _, c := range cards {
		verdicts = append(verdicts, probeAMDCardHealth(c))
	}
	return verdicts
}

func probeAMDCardHealth(c amdSysfsCard) GPUHealth {
	h := GPUHealth{Index: c.index, Vendor: "AMD", Healthy: true}

	// A runtime-suspended card (amdgpu runpm, BACO/D3cold) has no readable PCIe
	// config space and may fail SMU-backed reads by design. Leave it alone.
	if data, err := readSysfsFile(filepath.Join(c.devicePath, "power", "runtime_status")); err == nil &&
		strings.TrimSpace(string(data)) == "suspended" {
		return h
	}

	// PCIe link state.
	linkPath := filepath.Join(c.devicePath, "current_link_speed")
	if data, err := readSysfsFile(linkPath); err == nil {
		if strings.EqualFold(strings.TrimSpace(string(data)), "Unknown") {
			h.Healthy = false
			h.Reason = HealthReasonPCIeLinkDown
			h.Detail = "current_link_speed=Unknown"
			return h
		}
	} else if isDeviceIOError(err) {
		h.Healthy = false
		h.Reason = HealthReasonDeviceIOError
		h.Detail = fmt.Sprintf("current_link_speed: %v", trimPathErr(err))
		return h
	}

	// SMU/driver-backed sensors. A missing file is fine (older kernels, iGPUs);
	// only an I/O failure on a file that exists counts.
	for _, rel := range smuBackedFiles(c.devicePath) {
		if _, err := readSysfsFile(rel); err != nil && isDeviceIOError(err) {
			h.Healthy = false
			h.Reason = HealthReasonDeviceIOError
			h.Detail = fmt.Sprintf("%s: %v", relPath(c.devicePath, rel), trimPathErr(err))
			return h
		}
	}
	return h
}

// smuBackedFiles lists the sysfs attributes whose reads go through the SMU or
// the driver's power-management path.
func smuBackedFiles(devicePath string) []string {
	files := []string{filepath.Join(devicePath, "gpu_busy_percent")}
	hwmonDirs, _ := filepath.Glob(filepath.Join(devicePath, "hwmon", "hwmon*"))
	for _, dir := range hwmonDirs {
		files = append(files,
			filepath.Join(dir, "temp1_input"),
			filepath.Join(dir, "power1_average"),
			filepath.Join(dir, "power1_input"),
		)
	}
	return files
}

func relPath(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil {
		return rel
	}
	return p
}

// trimPathErr strips the "open /sys/...:" prefix so the annotation stays short.
func trimPathErr(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// evaluateGPUHealth folds the sysfs verdicts and this cycle's tool evidence
// into the node verdict, applies hysteresis, publishes metrics and returns
// what ProbeAndLabel should write on the node.
func (a *Agent) evaluateGPUHealth(ctx context.Context, cards []GPUHealth, now time.Time) NodeHealth {
	log := log.FromContext(ctx)
	s := a.health
	s.mu.Lock()
	defer s.mu.Unlock()

	// gpu-missing: a discrete card we enumerated earlier in this process is gone.
	discrete := len(cards)
	if s.lastDiscreteCount > 0 && discrete < s.lastDiscreteCount {
		for i := discrete; i < s.lastDiscreteCount; i++ {
			cards = append(cards, GPUHealth{
				Index:   i,
				Vendor:  "AMD",
				Healthy: false,
				Reason:  HealthReasonGPUMissing,
				Detail:  fmt.Sprintf("%d of %d discrete cards visible in sysfs", discrete, s.lastDiscreteCount),
			})
		}
	} else if discrete > 0 {
		s.lastDiscreteCount = discrete
	}

	// Tool evidence applies to every card the tools would have covered; when no
	// card could be enumerated at all, pin it on index 0 so the verdict still
	// has a metric series.
	applyTool := func(reason, detail string) {
		if len(cards) == 0 {
			cards = append(cards, GPUHealth{Index: 0, Vendor: a.toolVendor(), Healthy: false, Reason: reason, Detail: detail})
			return
		}
		for i := range cards {
			if cards[i].Healthy {
				cards[i].Healthy = false
				cards[i].Reason = reason
				cards[i].Detail = detail
			}
		}
	}
	for _, tool := range sortedKeys(s.cycleTimeouts) {
		applyTool(HealthReasonProbeTimeout, s.cycleTimeouts[tool])
	}
	for _, reason := range sortedKeys(s.cycleToolVerdict) {
		applyTool(reason, s.cycleToolVerdict[reason])
	}

	known := len(cards) > 0 || s.known
	if !known {
		return NodeHealth{Known: false}
	}
	s.known = true

	anyDegraded := false
	reasons := make([]string, 0, len(cards))
	for _, c := range cards {
		if !c.Healthy {
			anyDegraded = true
			reasons = append(reasons, c.String())
		}
	}
	reason := strings.Join(reasons, "; ")

	// Hysteresis.
	switch {
	case anyDegraded && !s.degraded:
		s.degraded = true
		s.since = now
		s.cleanStreak = 0
		log.Info("GPU health degraded", "reason", reason)
	case anyDegraded && s.degraded:
		s.cleanStreak = 0
	case !anyDegraded && s.degraded:
		s.cleanStreak++
		if s.cleanStreak >= healthRecoveryStreak {
			s.degraded = false
			s.since = now
			log.Info("GPU health recovered", "cleanCycles", s.cleanStreak)
		} else {
			log.Info("GPU health clean cycle while degraded", "cleanCycles", s.cleanStreak, "needed", healthRecoveryStreak)
		}
	case s.since.IsZero():
		s.since = now
	}
	if s.degraded {
		if reason != "" {
			s.reason = reason
		}
	} else {
		s.reason = ""
	}

	// Metrics: one series per card seen this cycle; a card that stopped
	// appearing keeps its last value until the agent restarts.
	nodeName := a.nodeName
	for _, c := range cards {
		key := c.Vendor + "/" + strconv.Itoa(c.Index)
		prev, seen := s.lastCards[key]
		val := 1.0
		if !c.Healthy {
			val = 0
		}
		metrics.GPUHealthy.WithLabelValues(strconv.Itoa(c.Index), nodeName, c.Vendor).Set(val)
		if !c.Healthy && (!seen || prev.Healthy) {
			metrics.GPUHealthDegradedTotal.WithLabelValues(strconv.Itoa(c.Index), nodeName, c.Vendor, c.Reason).Inc()
		}
		s.lastCards[key] = c
	}

	return NodeHealth{
		Known:    true,
		Degraded: s.degraded,
		Reason:   s.reason,
		Since:    s.since,
		Cards:    cards,
	}
}

// toolVendor guesses the vendor for a tool-only verdict from the node's labels
// cached on the agent; "AMD" when unknown, which matches every node in the
// fleet whose tools have ever hung.
func (a *Agent) toolVendor() string {
	if a.lastVendor != "" {
		return a.lastVendor
	}
	return "AMD"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// applyGPUHealth writes the sentinel's verdict into the label/annotation maps
// ProbeAndLabel patches onto the node. The keys are the fixed constants the
// scheduler extender and the controller read, independent of --label-prefix.
func applyGPUHealth(health NodeHealth, labels, annotations map[string]string) {
	if !health.Known {
		return
	}
	if health.Degraded {
		labels[constants.NodeLabelGPUHealth] = constants.GPUHealthDegraded
	} else {
		labels[constants.NodeLabelGPUHealth] = constants.GPUHealthOK
	}
	annotations[constants.NodeAnnotationGPUHealthReason] = health.Reason
	if !health.Since.IsZero() {
		annotations[constants.NodeAnnotationGPUHealthSince] = health.Since.UTC().Format(time.RFC3339)
	}
}

// hardDegraded reports whether any card failed a sysfs check that makes
// running vendor tools pointless and risky (they would join the processes
// stuck in state D on the dead device).
func hardDegraded(cards []GPUHealth) bool {
	for _, c := range cards {
		if !c.Healthy && (c.Reason == HealthReasonPCIeLinkDown || c.Reason == HealthReasonDeviceIOError) {
			return true
		}
	}
	return false
}

// ensureHealth lazily creates the sentinel state so zero-value Agents used in
// tests behave like NewAgent ones.
func (a *Agent) ensureHealth() {
	if a.health == nil {
		a.health = newHealthState()
	}
}
