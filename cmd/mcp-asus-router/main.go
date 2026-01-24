package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version              = "0.1.0"
	hostAlias            = getEnv("ASUS_ROUTER_HOST", "asus-router")
	hostPort             = getEnvInt("ASUS_ROUTER_PORT", 22)
	hostUser             = getEnv("ASUS_ROUTER_USER", "admin")
	routerTimeoutSeconds = getEnvInt("ASUS_ROUTER_TIMEOUT_SECONDS", 20)
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	server := mcp.NewServer("mcp-asus-router", version)
	server.SetInstructions("ASUS Router management via SSH")

	// Register tools
	registerTools(server)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func registerTools(server *mcp.Server) {
	server.AddTool(mcp.Tool{
		Name:        "router_status",
		Description: "Uptime, WAN, and memory utilization snapshot.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleStatus)

	server.AddTool(mcp.Tool{
		Name:        "router_logread",
		Description: "Tail BusyBox syslog (logread -n <lines>).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"lines": map[string]any{"type": "integer", "minimum": 10, "maximum": 2000},
			},
		},
	}, handleLogread)

	server.AddTool(mcp.Tool{
		Name:        "router_kernelTail",
		Description: "Tail kernel messages (dmesg | tail).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"lines": map[string]any{"type": "integer", "minimum": 10, "maximum": 500},
			},
		},
	}, handleKernelTail)

	server.AddTool(mcp.Tool{
		Name:        "router_execCommand",
		Description: "Run a whitelisted maintenance command",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"command": map[string]any{
					"type": "string",
					"enum": []string{"nvram-show", "ifconfig", "iptables", "memory", "cpu", "temperature"},
				},
			},
			Required: []string{"command"},
		},
	}, handleExecCommand)

	server.AddTool(mcp.Tool{
		Name:        "router_reboot",
		Description: "Reboot the ASUS router (requires confirm=true).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"confirm": map[string]any{"type": "boolean"},
			},
			Required: []string{"confirm"},
		},
	}, handleReboot)

	server.AddTool(mcp.Tool{
		Name:        "router_wanStatus",
		Description: "WAN IP, gateway, DNS, and link snapshot.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleWanStatus)

	server.AddTool(mcp.Tool{
		Name:        "router_wifiStatus",
		Description: "Wi-Fi chanspec/bandwidth and assoc list.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleWifiStatus)
}

// SSH Helper

func runRemote(ctx context.Context, cmd string) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && routerTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(routerTimeoutSeconds)*time.Second)
		defer cancel()
	}

	home, _ := os.UserHomeDir()
	controlPath := filepath.Join(home, ".ssh", "cm-asus-router-%r@%h:%p")
	os.MkdirAll(filepath.Dir(controlPath), 0700)

	args := []string{
		"-p", fmt.Sprintf("%d", hostPort),
		"-l", hostUser,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=3",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + controlPath,
		"-o", "ControlPersist=60",
		hostAlias,
		"sh", "-c", "PATH=/opt/bin:/opt/sbin:$PATH; " + cmd,
	}

	c := exec.CommandContext(ctx, "ssh", args...)
	out, err := c.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		if ctx.Err() != nil {
			if outStr == "" {
				return "", fmt.Errorf("ssh timed out: %w", ctx.Err())
			}
			return "", fmt.Errorf("ssh timed out: %w, output: %s", ctx.Err(), outStr)
		}
		if outStr == "" {
			return "", fmt.Errorf("ssh failed: %w", err)
		}
		return "", fmt.Errorf("ssh failed: %w, output: %s", err, outStr)
	}
	return strings.TrimSpace(string(out)), nil
}

// Handlers

func handleStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	script := `
{
  echo "# Uptime / Load";
  uptime;
  echo;
  echo "# Storage";
  df -h | grep -E '^Filesystem|/tmp|/jffs';
  echo;
  echo "# WAN IP";
  nvram get wan0_ipaddr;
  echo;
  echo "# Assoc Clients";
  wl -i eth6 assoclist 2>/dev/null || true;
} 2>&1
`
	out, err := runRemote(ctx, script)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleLogread(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	lines := 200
	if v, ok := args["lines"].(float64); ok {
		lines = int(v)
	}
	out, err := runRemote(ctx, fmt.Sprintf("logread -n %d", lines))
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleKernelTail(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	lines := 200
	if v, ok := args["lines"].(float64); ok {
		lines = int(v)
	}
	out, err := runRemote(ctx, fmt.Sprintf("dmesg | tail -n %d", lines))
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

var whitelistCommands = map[string]string{
	"nvram-show":  "nvram show",
	"ifconfig":    "/sbin/ifconfig -a",
	"iptables":    "iptables -L -v -n",
	"memory":      "cat /proc/meminfo",
	"cpu":         "cat /proc/cpuinfo",
	"temperature": "cat /sys/class/thermal/thermal_zone*/temp",
}

func handleExecCommand(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	cmdName, _ := args["command"].(string)
	cmd, ok := whitelistCommands[cmdName]
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("command not permitted")), nil
	}
	out, err := runRemote(ctx, cmd)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleReboot(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	confirm, _ := args["confirm"].(bool)
	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm=true required")), nil
	}
	_, err := runRemote(ctx, "reboot")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult("Router reboot triggered."), nil
}

func handleWanStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	script := `
{
  echo "# WAN status";
  echo "wan0_ipaddr: $(nvram get wan0_ipaddr 2>/dev/null)";
  echo "wan0_gateway: $(nvram get wan0_gateway 2>/dev/null)";
  echo "wan0_dns: $(nvram get wan0_dns 2>/dev/null)";
  echo "wan0_ifname: $(nvram get wan0_ifname 2>/dev/null)";
  echo;
  ifconfig eth0 2>/dev/null | head -n 5 || true;
} 2>&1
`
	out, err := runRemote(ctx, script)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleWifiStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	script := `
{
  for iface in eth6 eth7 eth8; do
    if ! ifconfig "$iface" >/dev/null 2>&1; then
      continue;
    fi;
    echo "## $iface";
    wl -i "$iface" chanspec 2>/dev/null || true;
    wl -i "$iface" channel 2>/dev/null || true;
    wl -i "$iface" bw_cap 2>/dev/null || true;
    wl -i "$iface" assoclist 2>/dev/null || true;
    echo;
  done;
} 2>&1
`
	out, err := runRemote(ctx, script)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}
