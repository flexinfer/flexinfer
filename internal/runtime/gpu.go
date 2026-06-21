package runtime

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
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

// QueryGPU queries GPU VRAM and temperature using vendor-specific CLI tools.
// Returns a best-effort GPUInfo (zero values on error).
func QueryGPU(vendor, arch string) GPUInfo {
	switch vendor {
	case "amd":
		return queryROCmSMI()
	case "nvidia":
		return queryNvidiaSMI()
	default:
		return GPUInfo{}
	}
}

// queryROCmSMI parses `rocm-smi --showmeminfo vram --showtemp --csv` output.
func queryROCmSMI() GPUInfo {
	info := GPUInfo{}

	// Query VRAM usage.
	out, err := exec.Command("rocm-smi", "--showmemuse", "--csv").Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		// CSV format: device,GPU memory use (%),  ...
		// Parse first GPU device line.
		for _, line := range lines[1:] {
			fields := strings.Split(line, ",")
			if len(fields) >= 2 {
				// rocm-smi --showmemuse gives percentage; need --showmeminfo for absolute.
				break
			}
		}
	}

	// Query VRAM absolute values.
	out, err = exec.Command("rocm-smi", "--showmeminfo", "vram", "--csv").Output()
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

	// Query temperature.
	out, err = exec.Command("rocm-smi", "--showtemp", "--csv").Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines[1:] {
			fields := strings.Split(line, ",")
			if len(fields) >= 2 {
				if temp, e := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64); e == nil {
					info.Temperature = temp
				}
				break
			}
		}
	}

	return info
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
