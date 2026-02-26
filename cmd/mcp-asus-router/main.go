package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version              = "0.1.0"
	hostAlias            = env.String("ASUS_ROUTER_HOST", "asus-router")
	hostPort             = env.Int("ASUS_ROUTER_PORT", 22)
	hostUser             = env.String("ASUS_ROUTER_USER", "admin")
	routerTimeoutSeconds = env.Int("ASUS_ROUTER_TIMEOUT_SECONDS", 20)
)

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-asus-router", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-asus-router")
	wrap := func(name string, h mcp.ToolHandler) mcp.ToolHandler {
		return mcpotel.TracedToolHandler(tracer, name, h)
	}
	logger.Info("starting server", "name", "mcp-asus-router", "version", version, "host", hostAlias)

	server := mcp.NewServer("mcp-asus-router", version)
	server.SetInstructions("ASUS Router management via SSH")

	// Register tools
	registerTools(server, wrap)

	return server.Run(ctx)
}

func registerTools(server *mcp.Server, wrap func(string, mcp.ToolHandler) mcp.ToolHandler) {
	server.AddTool(mcp.Tool{
		Name:        "router_status",
		Description: "Uptime, WAN, and memory utilization snapshot.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, wrap("router_status", handleStatus))

	server.AddTool(mcp.Tool{
		Name:        "router_logread",
		Description: "Tail BusyBox syslog (logread -n <lines>).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"lines": map[string]any{"type": "integer", "minimum": 10, "maximum": 2000},
			},
		},
	}, wrap("router_logread", handleLogread))

	server.AddTool(mcp.Tool{
		Name:        "router_kernelTail",
		Description: "Tail kernel messages (dmesg | tail).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"lines": map[string]any{"type": "integer", "minimum": 10, "maximum": 500},
			},
		},
	}, wrap("router_kernelTail", handleKernelTail))

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
	}, wrap("router_execCommand", handleExecCommand))

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
	}, wrap("router_reboot", handleReboot))

	server.AddTool(mcp.Tool{
		Name:        "router_wanStatus",
		Description: "WAN IP, gateway, DNS, and link snapshot.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, wrap("router_wanStatus", handleWanStatus))

	server.AddTool(mcp.Tool{
		Name:        "router_wifiStatus",
		Description: "Wi-Fi chanspec/bandwidth and assoc list.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, wrap("router_wifiStatus", handleWifiStatus))
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
	v := validate.NewArgs(args)
	lines := v.IntRange("lines", 200, 10, 2000)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	out, err := runRemote(ctx, fmt.Sprintf("logread -n %d", lines))
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleKernelTail(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	lines := v.IntRange("lines", 200, 10, 500)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
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
	v := validate.NewArgs(args)
	cmdName := v.Required("command")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
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
	v := validate.NewArgs(args)
	confirm := v.RequiredBool("confirm")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
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
