package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
)

// ErrInsufficientHWQueues means the GPU's hardware compute queues are
// committed; another subprocess must exit before the load can be retried.
var ErrInsufficientHWQueues = pkgrt.ErrInsufficientHWQueues

// kfdSysfsRoot is where KFD publishes its topology and, per process, the
// queues it created (/proc/<pid>/queues/<id>/{type,gpuid}). Reading it needs
// no debugfs; the privileged runtime pod sees every process on the host.
const kfdSysfsRoot = "/sys/class/kfd/kfd"

// hwQueueNoBudget is admitHWQueues' reason when no queue budget applies.
const hwQueueNoBudget = "no hardware queue budget"

// kfdQueueTypeCompute is KFD_QUEUE_TYPE_COMPUTE. SDMA queues (type 1) use the
// SDMA engines' own slots and do not count against num_cp_queues.
const kfdQueueTypeCompute = "0"

// HWQueuesRT reports the runtime's compute-queue accounting for its GPU:
// source="estimated" sums the hwQueueEstimate of every subprocess the runtime
// holds; source="observed" counts the compute queues KFD reports on the GPU
// across all processes (other pods included).
var HWQueuesRT = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "flexinfer_runtime_hw_queues",
		Help: "Hardware compute queues on the runtime's GPU: estimated for its subprocesses, or observed through KFD for all processes.",
	},
	[]string{"gpu_vendor", "gpu_arch", "source"},
)

// HWQueueBudgetRT reports the GPUProfile queue budget the runtime last
// received with a load request (kind="max" or "reserved").
var HWQueueBudgetRT = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "flexinfer_runtime_hw_queue_budget",
		Help: "GPUProfile hardware compute-queue budget the runtime admits loads against (max, reserved).",
	},
	[]string{"gpu_vendor", "gpu_arch", "kind"},
)

// hwQueueEstimateFor returns the compute queues a load is expected to hold:
// config.hwQueueEstimate when the Model sets one, otherwise the backend default
// for the GPU_MAX_HW_QUEUES the subprocess will see (the request env over the
// runtime's own environment).
func hwQueueEstimateFor(backendName string, req LoadRequest) int32 {
	if v, ok := configNonNegativeInt(req.Config, pkgrt.HWQueueEstimateConfigKey); ok {
		return int32(v)
	}
	var env []corev1.EnvVar
	if v, ok := os.LookupEnv(backend.EnvGPUMaxHWQueues); ok {
		env = append(env, corev1.EnvVar{Name: backend.EnvGPUMaxHWQueues, Value: v})
	}
	env = append(env, req.Env...)
	return backend.DefaultHWQueueEstimate(backendName, env)
}

func configNonNegativeInt(cfg map[string]any, key string) (int, bool) {
	raw, ok := cfg[key]
	if !ok {
		return 0, false
	}
	var v int
	switch n := raw.(type) {
	case int:
		v = n
	case int32:
		v = int(n)
	case int64:
		v = int(n)
	case float64:
		v = int(n)
	default:
		return 0, false
	}
	return v, v >= 0
}

// estimatedHWQueuesLocked sums the queue estimates of every tracked subprocess
// except exclude. Failed entries count too: a stalled or timed-out backend
// still holds its queues until it is unloaded. Caller must hold m.mu.
func (m *Manager) estimatedHWQueuesLocked(exclude string) int32 {
	var sum int32
	for name, lm := range m.models {
		if name != exclude {
			sum += lm.hwQueues
		}
	}
	return sum
}

// publishHWQueuesLocked refreshes the estimated-queue and budget gauges.
// Caller must hold m.mu.
func (m *Manager) publishHWQueuesLocked() {
	vendor, arch := string(m.gpuVendor), m.gpuArch
	HWQueuesRT.WithLabelValues(vendor, arch, "estimated").Set(float64(m.estimatedHWQueuesLocked("")))
	if m.hwQueueBudget != nil {
		HWQueueBudgetRT.WithLabelValues(vendor, arch, "max").Set(float64(m.hwQueueBudget.Max))
		HWQueueBudgetRT.WithLabelValues(vendor, arch, "reserved").Set(float64(m.hwQueueBudget.Reserved))
	}
}

// admitHWQueues decides whether a model with the given queue estimate fits the
// GPUProfile queue budget beside the subprocesses already running. The queues
// in use are the larger of (a) the estimates of the other tracked subprocesses
// plus the profile's reserve for tenants outside the Model API and (b) the
// compute queues KFD reports on this GPU, which also sees dedicated pods,
// Jobs and hand-written Deployments. Admission requires in-use + estimate <=
// max. Without a budget (the profile sets no maxHWQueues) it always admits;
// without KFD telemetry it falls back to (a) alone. budget is the request's;
// a request without one uses the last budget the runtime received.
func (m *Manager) admitHWQueues(logger logr.Logger, name string, estimate int32, budget *pkgrt.HWQueueBudget) (bool, string) {
	m.mu.Lock()
	if budget != nil && budget.Max > 0 {
		b := *budget
		m.hwQueueBudget = &b
	}
	budget = m.hwQueueBudget
	others := m.estimatedHWQueuesLocked(name)
	m.publishHWQueuesLocked()
	m.mu.Unlock()

	if budget == nil || budget.Max <= 0 {
		return true, hwQueueNoBudget
	}
	reserved := budget.Reserved
	if reserved < 0 {
		reserved = 0
	}
	used, source := others+reserved, "estimate"
	observed, observedOK := int32(0), false
	if m.hwQueueQuery != nil {
		observed, observedOK = m.hwQueueQuery()
	}
	if observedOK {
		HWQueuesRT.WithLabelValues(string(m.gpuVendor), m.gpuArch, "observed").Set(float64(observed))
		if observed > used {
			used, source = observed, "kfd"
		}
	}
	observedText := "unavailable"
	if observedOK {
		observedText = strconv.Itoa(int(observed))
	}
	detail := fmt.Sprintf("in use=%d (%s; subprocess estimate=%d + reserved=%d, KFD observed=%s) + estimate=%d, max=%d",
		used, source, others, reserved, observedText, estimate, budget.Max)
	if used+estimate > budget.Max {
		logger.Info("Hardware queue admission refused", "model", name, "detail", detail)
		return false, "insufficient hardware compute queues: " + detail
	}
	return true, "admit: " + detail
}

// ObservedHWQueues counts the compute queues KFD reports for every process on
// the GPU(s) of the given architecture. ok is false when the KFD sysfs tree is
// missing or no GPU node matches the architecture.
func ObservedHWQueues(gpuArch string) (int32, bool) {
	return observeKFDComputeQueues(kfdSysfsRoot, gpuArch)
}

func observeKFDComputeQueues(root, gpuArch string) (int32, bool) {
	target, ok := gfxTargetVersion(gpuArch)
	if !ok {
		return 0, false
	}
	gpuIDs := kfdGPUIDsForTarget(root, target)
	if len(gpuIDs) == 0 {
		return 0, false
	}
	procs, err := os.ReadDir(filepath.Join(root, "proc"))
	if err != nil {
		return 0, false
	}
	var count int32
	for _, proc := range procs {
		queuesDir := filepath.Join(root, "proc", proc.Name(), "queues")
		queues, err := os.ReadDir(queuesDir)
		if err != nil {
			// The process exited between the two reads, or has no queues.
			continue
		}
		for _, q := range queues {
			qType := readTrimmed(filepath.Join(queuesDir, q.Name(), "type"))
			gpuID := readTrimmed(filepath.Join(queuesDir, q.Name(), "gpuid"))
			if qType == kfdQueueTypeCompute && gpuIDs[gpuID] {
				count++
			}
		}
	}
	return count, true
}

// kfdGPUIDsForTarget returns the KFD gpu_id of every topology node whose
// gfx_target_version equals target.
func kfdGPUIDsForTarget(root string, target int) map[string]bool {
	nodes, err := os.ReadDir(filepath.Join(root, "topology", "nodes"))
	if err != nil {
		return nil
	}
	ids := make(map[string]bool)
	for _, node := range nodes {
		dir := filepath.Join(root, "topology", "nodes", node.Name())
		gpuID := readTrimmed(filepath.Join(dir, "gpu_id"))
		if gpuID == "" || gpuID == "0" {
			continue // CPU node
		}
		props, err := os.ReadFile(filepath.Join(dir, "properties"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(props), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "gfx_target_version" {
				if v, err := strconv.Atoi(fields[1]); err == nil && v == target {
					ids[gpuID] = true
				}
			}
		}
	}
	return ids
}

// gfxTargetVersion maps an LLVM target name to KFD's gfx_target_version:
// major*10000 + minor*100 + stepping, where the last two characters are hex
// digits (gfx906 -> 90006, gfx90a -> 90010, gfx1100 -> 110000).
func gfxTargetVersion(arch string) (int, bool) {
	arch = strings.ToLower(strings.TrimSpace(arch))
	if i := strings.IndexByte(arch, ':'); i >= 0 {
		arch = arch[:i] // drop target features such as :sramecc+
	}
	digits, ok := strings.CutPrefix(arch, "gfx")
	if !ok || len(digits) < 3 {
		return 0, false
	}
	major, err := strconv.Atoi(digits[:len(digits)-2])
	if err != nil {
		return 0, false
	}
	minor, err := strconv.ParseInt(digits[len(digits)-2:len(digits)-1], 16, 0)
	if err != nil {
		return 0, false
	}
	stepping, err := strconv.ParseInt(digits[len(digits)-1:], 16, 0)
	if err != nil {
		return 0, false
	}
	return major*10000 + int(minor)*100 + int(stepping), true
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
