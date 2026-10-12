package runtime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GPUInfo holds GPU device information queried from vendor tools.
type GPUInfo struct {
	VRAMTotalMB int64   `json:"vramTotalMB"`
	VRAMUsedMB  int64   `json:"vramUsedMB"`
	VRAMFreeMB  int64   `json:"vramFreeMB"`
	Temperature float64 `json:"temperature"`
}

// QueryGPU queries GPU VRAM and temperature. On AMD it reads the amdgpu sysfs
// counters of the visible device first and falls back to rocm-smi; on NVIDIA
// it uses nvidia-smi. Returns a best-effort GPUInfo (zero values on error).
func QueryGPU(vendor, arch string) GPUInfo {
	switch vendor {
	case "amd":
		if info, ok := readSysfsVRAM("/sys", "/dev", os.Getenv); ok {
			if info.Temperature == 0 {
				info.Temperature = rocmSMITemperature()
			}
			return info
		}
		return queryROCmSMI(true)
	case "nvidia":
		return queryNvidiaSMI()
	default:
		return GPUInfo{}
	}
}

// queryVRAM is QueryGPU without the temperature lookup, for the manager's
// admission and drain polling where an extra rocm-smi exec per sample would
// dominate the wait.
func queryVRAM(vendor string) GPUInfo {
	if vendor == "amd" {
		if info, ok := readSysfsVRAM("/sys", "/dev", os.Getenv); ok {
			return info
		}
		return queryROCmSMI(false)
	}
	return QueryGPU(vendor, "")
}

// readSysfsVRAM reads mem_info_vram_total/mem_info_vram_used (and the edge
// temperature from hwmon) for the AMD GPU this process would use. Candidates
// are the render nodes present under devRoot/dri — inside a pod that is the
// device-plugin-granted set, so ROCm's device 0 is the first of them — falling
// back to every render node in sysfs. Nodes without amdgpu VRAM counters
// (e.g. NVIDIA render nodes) are skipped. ROCR_VISIBLE_DEVICES then
// HIP_VISIBLE_DEVICES select among the remaining devices by index, mirroring
// how ROCr and HIP compose them. It returns ok=false when no device matches so
// the caller can fall back to rocm-smi.
//
// The sysfs counters are the same source rocm-smi parses, without its ~0.5 s
// Python start-up per call and without the D-state hangs seen on the 7900xtx
// host, which matters now that unload polls telemetry while draining VRAM.
func readSysfsVRAM(sysRoot, devRoot string, getenv func(string) string) (GPUInfo, bool) {
	type device struct {
		minor int
		dir   string
	}
	collect := func(dir string) []device {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		var out []device
		for _, e := range entries {
			minor, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "renderD"))
			if err != nil || !strings.HasPrefix(e.Name(), "renderD") {
				continue
			}
			base := filepath.Join(sysRoot, "class", "drm", e.Name(), "device")
			if total, ok := readSysfsInt(filepath.Join(base, "mem_info_vram_total")); ok && total > 0 {
				out = append(out, device{minor: minor, dir: base})
			}
		}
		return out
	}
	devices := collect(filepath.Join(devRoot, "dri"))
	if len(devices) == 0 {
		devices = collect(filepath.Join(sysRoot, "class", "drm"))
	}
	if len(devices) == 0 {
		return GPUInfo{}, false
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].minor < devices[j].minor })
	for _, key := range []string{"ROCR_VISIBLE_DEVICES", "HIP_VISIBLE_DEVICES"} {
		indices, ok := visibleIndices(getenv(key))
		if !ok {
			continue
		}
		visible := make([]device, 0, len(indices))
		for _, idx := range indices {
			if idx >= len(devices) {
				return GPUInfo{}, false
			}
			visible = append(visible, devices[idx])
		}
		devices = visible
	}
	dev := devices[0]
	total, _ := readSysfsInt(filepath.Join(dev.dir, "mem_info_vram_total"))
	used, ok := readSysfsInt(filepath.Join(dev.dir, "mem_info_vram_used"))
	if !ok {
		return GPUInfo{}, false
	}
	info := GPUInfo{
		VRAMTotalMB: total / (1024 * 1024),
		VRAMUsedMB:  used / (1024 * 1024),
	}
	info.VRAMFreeMB = info.VRAMTotalMB - info.VRAMUsedMB
	if temps, _ := filepath.Glob(filepath.Join(dev.dir, "hwmon", "hwmon*", "temp1_input")); len(temps) > 0 {
		if milli, ok := readSysfsInt(temps[0]); ok {
			info.Temperature = float64(milli) / 1000
		}
	}
	return info, true
}

// visibleIndices parses a *_VISIBLE_DEVICES list of device indices. Empty,
// UUID-style or otherwise non-numeric lists are ignored (ok=false).
func visibleIndices(v string) ([]int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, false
	}
	var out []int
	for _, field := range strings.Split(v, ",") {
		idx, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || idx < 0 {
			return nil, false
		}
		out = append(out, idx)
	}
	return out, true
}

func readSysfsInt(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// vramTelemetryPlausible reports whether a VRAM reading can be trusted for
// admission and drain decisions. readyEstimateMB is the summed vramEstimateMB of
// models the runtime believes are Ready on the GPU: a used reading below half of
// it means the counter is not seeing those allocations (wrong device, broken
// telemetry, or backends that silently fell back to CPU), so callers treat it
// as unknown and fail open rather than act on it. reason is a short label.
func vramTelemetryPlausible(info GPUInfo, readyEstimateMB int64) (ok bool, reason string) {
	switch {
	case info.VRAMTotalMB <= 0:
		return false, "absent"
	case info.VRAMUsedMB < 0 || info.VRAMUsedMB > info.VRAMTotalMB:
		return false, "out_of_range"
	case readyEstimateMB > 0 && info.VRAMUsedMB < readyEstimateMB/2:
		return false, "below_loaded_estimate"
	default:
		return true, ""
	}
}

// rocmSMITimeout bounds each rocm-smi invocation; it has been seen hanging.
const rocmSMITimeout = 10 * time.Second

func rocmSMIOutput(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rocmSMITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rocm-smi", args...)
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// queryROCmSMI parses `rocm-smi --showmeminfo vram --csv` (and, withTemp,
// `rocm-smi --showtemp --csv`) output for the first listed device.
func queryROCmSMI(withTemp bool) GPUInfo {
	info := GPUInfo{}

	// Query VRAM absolute values.
	out, err := rocmSMIOutput("--showmeminfo", "vram", "--csv")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines[1:] {
			fields := strings.Split(line, ",")
			if len(fields) >= 3 {
				// Fields: device, VRAM Total (B), VRAM Used (B)
				if total, e := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64); e == nil {
					info.VRAMTotalMB = total / (1024 * 1024)
				}
				if used, e := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64); e == nil {
					info.VRAMUsedMB = used / (1024 * 1024)
				}
				info.VRAMFreeMB = info.VRAMTotalMB - info.VRAMUsedMB
				break
			}
		}
	}

	if withTemp {
		info.Temperature = rocmSMITemperature()
	}
	return info
}

// rocmSMITemperature returns the first device's edge temperature from
// `rocm-smi --showtemp --csv`, or 0 when unavailable.
func rocmSMITemperature() float64 {
	out, err := rocmSMIOutput("--showtemp", "--csv")
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines[1:] {
		fields := strings.Split(line, ",")
		if len(fields) >= 2 {
			if temp, e := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64); e == nil {
				return temp
			}
			break
		}
	}
	return 0
}

// queryNvidiaSMI parses `nvidia-smi --query-gpu=... --format=csv,noheader` output.
func queryNvidiaSMI() GPUInfo {
	info := GPUInfo{}

	out, err := exec.Command(
		"nvidia-smi",
		"--query-gpu=memory.total,memory.used,memory.free,temperature.gpu",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return info
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return info
	}

	fields := strings.Split(lines[0], ",")
	if len(fields) >= 4 {
		if v, e := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64); e == nil {
			info.VRAMTotalMB = v
		}
		if v, e := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64); e == nil {
			info.VRAMUsedMB = v
		}
		if v, e := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64); e == nil {
			info.VRAMFreeMB = v
		}
		if v, e := strconv.ParseFloat(strings.TrimSpace(fields[3]), 64); e == nil {
			info.Temperature = v
		}
	}

	return info
}

// checkHTTPHealth performs a simple HTTP GET and returns true if status is 200.
func checkHTTPHealth(url string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// checkTCPHealth returns true if a TCP connection to addr can be established.
// Used for backends whose readiness probe is a TCP socket check (no HTTP
// endpoint), e.g. ComfyUI (WebSocket) and the Steam backend.
func checkTCPHealth(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// GPUDeviceAccessible returns true if the GPU device path is accessible.
// For AMD: checks /dev/dri/renderD128. For NVIDIA: checks nvidia-smi.
func GPUDeviceAccessible(vendor string) bool {
	switch vendor {
	case "amd":
		return fileExists("/dev/dri/renderD128")
	case "nvidia":
		return exec.Command("nvidia-smi").Run() == nil
	default:
		return false
	}
}

func fileExists(path string) bool {
	_, err := exec.Command("test", "-e", path).Output()
	if err != nil {
		// Fallback: try stat.
		cmd := fmt.Sprintf("[ -e %s ]", path)
		return exec.Command("sh", "-c", cmd).Run() == nil
	}
	return true
}
