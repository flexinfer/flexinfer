package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "0.1.0"
var hostsConfigPath string

// Host represents a server configuration
type Host struct {
	Name string   `toml:"-" json:"name"`
	Host string   `toml:"host" json:"host"`
	User string   `toml:"user,omitempty" json:"user,omitempty"`
	Port int      `toml:"port,omitempty" json:"port,omitempty"`
	Key  string   `toml:"key,omitempty" json:"key,omitempty"`
	OS   string   `toml:"os,omitempty" json:"os,omitempty"`
	Tags []string `toml:"tags,omitempty" json:"tags,omitempty"`
}

// Config represents the servers.toml structure
type Config struct {
	Hosts map[string]Host `toml:"hosts"`
}

var hosts map[string]Host

func main() {
	// Load hosts
	if err := loadHosts(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading hosts: %v\n", err)
		os.Exit(1)
	}

	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp,
		shutdownTracer,

		err :=
		mcpotel.InitTracer(ctx, "server-mgmt",

			logger)

	if err != nil {
		logger.Warn("OTel tracer init failed",
			"error", err)
	}

	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "server-mgmt")

	logger.Info("starting server", "name", "mcp-server-mgmt", "version", version)

	server := mcp.NewServer("server-mgmt", version)
	server.SetInstructions("SSH-based Linux server management")

	// Register tools
	server.AddTool(mcp.Tool{
		Name:        "server_listHosts",
		Description: "List configured SSH hosts",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, mcpotel.TracedToolHandler(tracer, "server_listHosts", handleListHosts))

	server.AddTool(mcp.Tool{
		Name:        "server_getHost",
		Description: "Get a specific host config",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{"name": map[string]any{"type": "string"}},
			Required:   []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "server_getHost", handleGetHost))

	server.AddTool(mcp.Tool{
		Name:        "server_detectOS",
		Description: "Detect OS via /etc/os-release",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{"host": map[string]any{"type": "string"}},
			Required:   []string{"host"},
		},
	}, mcpotel.TracedToolHandler(tracer, "server_detectOS", handleDetectOS))

	server.AddTool(mcp.Tool{
		Name:        "server_sshCommand",
		Description: "Return a ready-to-run ssh command",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"host":    map[string]any{"type": "string"},
				"command": map[string]any{"type": "string"},
			},
			Required: []string{"host", "command"},
		},
	}, mcpotel.TracedToolHandler(tracer, "server_sshCommand", handleSSHCommand))

	server.AddTool(mcp.Tool{
		Name:        "server_execSafe",
		Description: "Run a whitelisted command",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"host": map[string]any{"type": "string"},
				"name": map[string]any{"type": "string"},
				"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			Required: []string{"host", "name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "server_execSafe", handleExecSafe))

	return server.Run(ctx)
}

func loadHosts() error {
	hosts = make(map[string]Host)
	hostsConfigPath = ""

	configPath := findHostsConfigPath()
	if configPath == "" {
		return nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	for name, h := range cfg.Hosts {
		h.Name = name
		if h.Host == "" {
			h.Host = name
		}
		hosts[name] = h
	}
	hostsConfigPath = configPath
	return nil
}

func findHostsConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("LOOM_SERVER_MGMT_HOSTS_TOML")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	relCandidates := []string{
		filepath.Join("platform", "gitops", "scripts", "mcp", "servers.toml"),
		filepath.Join("scripts", "mcp", "servers.toml"),
		"servers.toml",
	}

	if cwd, err := os.Getwd(); err == nil {
		if p := findUpwards(cwd, relCandidates); p != "" {
			return p
		}
	}

	if exe, err := os.Executable(); err == nil {
		if p := findUpwards(filepath.Dir(exe), relCandidates); p != "" {
			return p
		}
	}

	// Backwards-compat relative paths (best effort)
	fallbackRel := []string{
		filepath.Join("..", "..", "platform", "gitops", "scripts", "mcp", "servers.toml"),
		filepath.Join("..", "..", "scripts", "mcp", "servers.toml"),
	}
	for _, p := range fallbackRel {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

func findUpwards(startDir string, relCandidates []string) string {
	dir := startDir
	for {
		for _, rel := range relCandidates {
			p := filepath.Join(dir, rel)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func getHost(name string) (Host, error) {
	h, ok := hosts[name]
	if !ok {
		return Host{}, fmt.Errorf("unknown host: %s", name)
	}
	return h, nil
}

func sshBase(h Host) []string {
	cmd := []string{
		"ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if h.Port != 0 {
		cmd = append(cmd, "-p", fmt.Sprintf("%d", h.Port))
	}
	if h.Key != "" {
		cmd = append(cmd, "-i", h.Key)
	}
	target := h.Host
	if h.User != "" {
		target = fmt.Sprintf("%s@%s", h.User, target)
	}
	cmd = append(cmd, target)
	return cmd
}

func runSSH(ctx context.Context, h Host, remoteArgv []string, timeout time.Duration) (string, string, error) {
	// Quote arguments
	var quoted []string
	for _, arg := range remoteArgv {
		quoted = append(quoted, fmt.Sprintf("'%s'", strings.ReplaceAll(arg, "'", "'\\''")))
	}
	remoteCmd := strings.Join(quoted, " ")

	args := sshBase(h)
	args = append(args, "sh", "-lc", remoteCmd)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Handlers

func handleListHosts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	list := make([]Host, 0, len(hosts))
	for _, h := range hosts {
		list = append(list, h)
	}
	return mcp.JSONResult(map[string]any{"hosts": list})
}

func handleGetHost(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	h, err := getHost(name)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(map[string]any{"host": h})
}

func handleDetectOS(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("host")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	h, err := getHost(name)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	if h.OS != "" {
		return mcp.JSONResult(map[string]any{"os": h.OS})
	}

	stdout, stderr, err := runSSH(ctx, h, []string{"cat", "/etc/os-release"}, 30*time.Second)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("ssh failed: %v\nstderr: %s", err, stderr)), nil
	}

	lower := strings.ToLower(stdout)
	var osID string
	if strings.Contains(lower, "ubuntu") {
		osID = "ubuntu"
	} else if strings.Contains(lower, "fedora") {
		osID = "fedora"
	} else if strings.Contains(lower, "sles") || strings.Contains(lower, "suse") {
		osID = "suse"
	} else if strings.Contains(lower, "harvester") {
		osID = "harvester"
	}

	if osID == "" {
		return mcp.ErrorResult(fmt.Errorf("failed to detect OS from output: %s", stdout)), nil
	}

	return mcp.JSONResult(map[string]any{"os": osID})
}

func handleSSHCommand(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("host")
	command := v.Required("command")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	h, err := getHost(name)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	base := sshBase(h)
	// Simple joining for display
	fullCmd := fmt.Sprintf("%s sh -lc '%s'", strings.Join(base, " "), strings.ReplaceAll(command, "'", "'\\''"))

	return mcp.JSONResult(map[string]any{"command": fullCmd})
}

var safeCommands = map[string][]string{
	"uptime":     {"uptime"},
	"df":         {"df", "-hT"},
	"free":       {"free", "-h"},
	"ip_addr":    {"ip", "-br", "addr"},
	"ip_route":   {"ip", "route"},
	"lsblk":      {"lsblk", "-o", "NAME,SIZE,TYPE,MOUNTPOINT"},
	"lscpu":      {"lscpu"},
	"os_release": {"cat", "/etc/os-release"},
}

func handleExecSafe(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	hostName := v.Required("host")
	cmdName := v.Required("name")
	cmdArgs := v.StringSlice("args")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	h, err := getHost(hostName)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	baseCmd, ok := safeCommands[cmdName]
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("unknown or disallowed command: %s", cmdName)), nil
	}

	fullCmd := make([]string, len(baseCmd))
	copy(fullCmd, baseCmd)
	fullCmd = append(fullCmd, cmdArgs...)

	stdout, stderr, err := runSSH(ctx, h, fullCmd, 60*time.Second)

	result := map[string]any{
		"ok":     err == nil,
		"stdout": stdout,
		"stderr": stderr,
	}
	if err != nil {
		result["error"] = err.Error()
	}

	return mcp.JSONResult(result)
}
