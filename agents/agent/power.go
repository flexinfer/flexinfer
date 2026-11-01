package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// GPU power sampling (D7, .loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.7).
//
// The sampler reads the amdgpu hwmon power sensor of every discrete card at a
// fixed interval and exports it as flexinfer_gpu_power_watts. It is off unless
// the node is named in --gpu-power-nodes: the RX 7900 XTX in cblevins-7900xtx
// has dropped off the PCIe bus under load, and hwmon power reads go through
// the SMU, so a 1 Hz reader is a new load on exactly the firmware path that
// wedged. The health sentinel already reads power1_average once per probe
// cycle (smuBackedFiles); this raises that rate, nothing else.
//
// Safety rules, in order:
//
//   - read-only sysfs, never rocm-smi or amd-smi (those are the tools that sat
//     in state D on the wedged card);
//   - a runtime-suspended card is skipped, not woken;
//   - the sampler stops for good (until the agent restarts) the first time the
//     sentinel reports the node degraded, a read fails with a device I/O
//     error, or a card's reads fail powerMaxConsecutiveErrors times in a row.
//     Stopping is logged with the reason, the power series are deleted and
//     flexinfer_gpu_power_sampler_active drops to 0.
//
// Lane energy (flexinfer_lane_energy_joules_total) integrates the same
// samples. Attribution rule: the card's energy between two consecutive good
// samples, (P_prev + P_now) / 2 × Δt, is split equally among the lanes Active
// on the card at the later sample (LaneSource; Models in phase Ready on this
// node that allocate a GPU). With no Active lane it is attributed to nobody:
// an idle card's draw is no lane's cost. On a shared card (the Radeon VII
// holds four co-Active retrieval and voice lanes) an equal split overstates
// idle co-residents and understates the busy one; it is the simple rule, and
// sum by (node) of the counter still equals the card's energy while any lane
// is Active. A node with more than one sampled card attributes nothing,
// because the agent cannot tell which card a lane holds. Gaps longer than
// powerMaxGapIntervals sampling periods (a stalled loop, a suspended card, a
// failed read) are not integrated.

const (
	// DefaultPowerInterval is the sampling period when sampling is enabled.
	DefaultPowerInterval = time.Second
	// minPowerInterval bounds --gpu-power-interval from below. amdgpu
	// refreshes power1_average about once a second, so a faster reader only
	// adds SMU traffic.
	minPowerInterval = 500 * time.Millisecond
	// powerMaxConsecutiveErrors is how many failed reads in a row (missing
	// file, unparsable or implausible value) stop the sampler.
	powerMaxConsecutiveErrors = 5
	// maxPlausibleWatts rejects readings no card in the fleet can produce
	// (a 7900 XTX is a 355 W board, a Radeon VII 300 W).
	maxPlausibleWatts = 1000.0
	// powerMaxGapIntervals bounds the interval one energy step may cover.
	powerMaxGapIntervals = 5
	// laneRefreshInterval is how often the Active lane set is re-listed; a
	// lane that turns Ready or parks is attributed at most this late.
	laneRefreshInterval = 15 * time.Second
	laneListTimeout     = 5 * time.Second
	// laneListMaxFailures consecutive failed listings clear the lane set, so
	// a lost API connection stops attribution instead of freezing it.
	laneListMaxFailures = 3
)

// Reasons the power sampler stops itself.
const (
	PowerStopHealthDegraded = "gpu-health-degraded"
	PowerStopDeviceIOError  = "device-io-error"
	PowerStopReadErrors     = "repeated-read-errors"
	PowerStopNoSensor       = "no-power-sensor"
	PowerStopNoSentinel     = "health-sentinel-off"
)

// PowerSamplingEnabled reports whether nodeName is listed in the
// comma-separated --gpu-power-nodes value. There is no wildcard: every node is
// an explicit opt-in.
func PowerSamplingEnabled(nodeName, nodes string) bool {
	if nodeName == "" {
		return false
	}
	for _, n := range strings.Split(nodes, ",") {
		if strings.TrimSpace(n) == nodeName {
			return true
		}
	}
	return false
}

// powerSensor is one discrete card's hwmon power attribute.
type powerSensor struct {
	index      int
	devicePath string
	path       string // .../hwmon/hwmonN/power1_average or power1_input
	errors     int    // consecutive failed reads

	// Last good sample, for the energy integral. Zero after a gap.
	lastAt    time.Time
	lastWatts float64
}

// PowerSampler samples GPU board power from sysfs. Create it with
// NewPowerSampler and call Run in its own goroutine.
type PowerSampler struct {
	agent    *Agent
	interval time.Duration
	log      logr.Logger

	sensors []powerSensor
	stopped bool

	lanes        LaneSource
	attributing  bool
	mu           sync.Mutex
	active       []string
	laneFailures int
}

// NewPowerSampler returns a sampler for the agent's node. interval is clamped
// to minPowerInterval; non-positive selects DefaultPowerInterval.
func (a *Agent) NewPowerSampler(interval time.Duration, log logr.Logger) *PowerSampler {
	switch {
	case interval <= 0:
		interval = DefaultPowerInterval
	case interval < minPowerInterval:
		interval = minPowerInterval
	}
	a.ensureHealth()
	return &PowerSampler{agent: a, interval: interval, log: log}
}

// SetLaneSource enables lane energy attribution. Call before Run.
func (p *PowerSampler) SetLaneSource(src LaneSource) { p.lanes = src }

// Interval is the effective sampling period.
func (p *PowerSampler) Interval() time.Duration { return p.interval }

// Run samples until ctx is done or the sampler stops itself.
func (p *PowerSampler) Run(ctx context.Context) {
	if reason, detail := p.start(); reason != "" {
		p.stop(reason, detail)
		return
	}
	if p.attributing {
		p.refreshLanes(ctx)
		go p.refreshLanesLoop(ctx)
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	p.sample(time.Now())
	for !p.stopped {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			p.sample(now)
		}
	}
}

// start resolves one power attribute per discrete amdgpu card.
func (p *PowerSampler) start() (reason, detail string) {
	if !p.agent.healthSentinel {
		return PowerStopNoSentinel, "power sampling self-disables on the sentinel's verdict; run with --gpu-health-sentinel"
	}
	for _, c := range p.agent.discreteAMDSysfsCards() {
		if path := hwmonPowerPath(c.devicePath); path != "" {
			p.sensors = append(p.sensors, powerSensor{index: c.index, devicePath: c.devicePath, path: path})
		}
	}
	if len(p.sensors) == 0 {
		return PowerStopNoSensor, "no discrete amdgpu card exposes hwmon power1_average or power1_input"
	}
	metrics.GPUPowerSamplerActive.WithLabelValues(p.agent.nodeName).Set(1)
	for _, s := range p.sensors {
		p.log.Info("GPU power sampling enabled", "gpu", s.index, "sensor", relPath(s.devicePath, s.path), "interval", p.interval)
	}
	switch {
	case p.lanes == nil:
	case len(p.sensors) > 1:
		p.log.Info("Lane energy attribution off: more than one sampled card, and the agent cannot tell which card a lane holds", "cards", len(p.sensors))
	default:
		p.attributing = true
	}
	return "", ""
}

// hwmonPowerPath prefers power1_average (the SMU's averaged board power, what
// rocm-smi reports) and falls back to power1_input, which some kernels expose
// for RDNA3 instead. Empty when neither exists.
func hwmonPowerPath(devicePath string) string {
	dirs, _ := filepath.Glob(filepath.Join(devicePath, "hwmon", "hwmon*"))
	for _, name := range []string{"power1_average", "power1_input"} {
		for _, dir := range dirs {
			path := filepath.Join(dir, name)
			if _, err := readSysfsFile(path); err == nil {
				return path
			}
		}
	}
	return ""
}

// sample takes one reading of every sensor. It is the unit tests' entry point.
func (p *PowerSampler) sample(now time.Time) {
	if p.stopped {
		return
	}
	if degraded, reason := p.agent.health.verdict(); degraded {
		p.stop(PowerStopHealthDegraded, reason)
		return
	}
	for i := range p.sensors {
		s := &p.sensors[i]
		if cardSuspended(s.devicePath) {
			s.lastAt = time.Time{}
			continue
		}
		watts, err := readPowerWatts(s.path)
		if err != nil {
			s.lastAt = time.Time{}
			if isDeviceIOError(err) {
				p.stop(PowerStopDeviceIOError, fmt.Sprintf("card%d %s: %v", s.index, relPath(s.devicePath, s.path), trimPathErr(err)))
				return
			}
			s.errors++
			if s.errors >= powerMaxConsecutiveErrors {
				p.stop(PowerStopReadErrors, fmt.Sprintf("card%d: %d consecutive failed reads, last: %v", s.index, s.errors, trimPathErr(err)))
				return
			}
			continue
		}
		s.errors = 0
		metrics.GPUPowerWatts.WithLabelValues(strconv.Itoa(s.index), p.agent.nodeName).Set(watts)
		if !s.lastAt.IsZero() {
			if dt := now.Sub(s.lastAt); dt > 0 && dt <= powerMaxGapIntervals*p.interval {
				p.attribute(s, (s.lastWatts+watts)/2*dt.Seconds())
			}
		}
		s.lastAt, s.lastWatts = now, watts
	}
}

// attribute splits one energy step equally among the Active lanes.
func (p *PowerSampler) attribute(s *powerSensor, joules float64) {
	if !p.attributing || joules <= 0 {
		return
	}
	p.mu.Lock()
	lanes := p.active
	p.mu.Unlock()
	if len(lanes) == 0 {
		return
	}
	share := joules / float64(len(lanes))
	gpu := strconv.Itoa(s.index)
	for _, lane := range lanes {
		metrics.LaneEnergyJoulesTotal.WithLabelValues(lane, p.agent.nodeName, gpu).Add(share)
	}
}

func (p *PowerSampler) refreshLanesLoop(ctx context.Context) {
	ticker := time.NewTicker(laneRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refreshLanes(ctx)
		}
	}
}

// refreshLanes re-lists the Active lanes. A failed listing keeps the last set
// until laneListMaxFailures in a row, then clears it.
func (p *PowerSampler) refreshLanes(ctx context.Context) {
	lctx, cancel := context.WithTimeout(ctx, laneListTimeout)
	defer cancel()
	lanes, err := p.lanes.ActiveLanes(lctx, p.agent.nodeName)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.laneFailures++
		if p.laneFailures == laneListMaxFailures {
			p.active = nil
			p.log.Info("Lane energy attribution paused: cannot list Active lanes", "error", err.Error(), "failures", p.laneFailures)
		}
		return
	}
	if p.laneFailures >= laneListMaxFailures {
		p.log.Info("Lane energy attribution resumed")
	}
	p.laneFailures = 0
	p.active = lanes
}

// stop latches the sampler off. It does not re-arm when the card recovers: a
// card that wedged is not read at 1 Hz again until someone restarts the agent.
func (p *PowerSampler) stop(reason, detail string) {
	p.stopped = true
	node := p.agent.nodeName
	for _, s := range p.sensors {
		metrics.GPUPowerWatts.DeleteLabelValues(strconv.Itoa(s.index), node)
	}
	metrics.GPUPowerSamplerActive.WithLabelValues(node).Set(0)
	p.log.Info("GPU power sampling stopped; restart the agent to re-enable", "reason", reason, "detail", detail)
}

// Stopped reports whether the sampler has disabled itself.
func (p *PowerSampler) Stopped() bool { return p.stopped }

// cardSuspended reports a runtime-suspended card (BACO/D3cold), whose SMU
// reads would either fail by design or wake the card.
func cardSuspended(devicePath string) bool {
	data, err := readSysfsFile(filepath.Join(devicePath, "power", "runtime_status"))
	return err == nil && strings.TrimSpace(string(data)) == "suspended"
}

var errImplausiblePower = errors.New("implausible power reading")

// readPowerWatts reads a hwmon power attribute (microwatts) as watts.
func readPowerWatts(path string) (float64, error) {
	data, err := readSysfsFile(path)
	if err != nil {
		return 0, err
	}
	uw, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}
	watts := float64(uw) / 1e6
	if watts < 0 || watts > maxPlausibleWatts {
		return 0, fmt.Errorf("%w: %.1f W", errImplausiblePower, watts)
	}
	return watts, nil
}
