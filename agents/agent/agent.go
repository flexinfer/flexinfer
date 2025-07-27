// Package agent implements the FlexInfer node agent, which is responsible for
// detecting hardware capabilities on a node and reporting them as labels.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/cpu"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Agent discovers node capabilities and applies them as labels.
type Agent struct {
	kubeClient  kubernetes.Interface
	nodeName    string
	labelPrefix string
	runCmd      func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// NewAgent creates a new Agent.
func NewAgent(labelPrefix string) (*Agent, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return nil, fmt.Errorf("NODE_NAME environment variable not set")
	}

	return &Agent{
		kubeClient:  clientset,
		nodeName:    nodeName,
		labelPrefix: labelPrefix,
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
	}, nil
}

// ProbeAndLabel detects hardware and updates node labels.
func (a *Agent) ProbeAndLabel(ctx context.Context) error {
	log := log.FromContext(ctx)
	log.Info("Probing for hardware capabilities...")

	labels := make(map[string]string)
	a.detectGPU(ctx, labels)
	a.detectCPU(labels)

	log.Info("Applying labels", "labels", labels)

	node, err := a.kubeClient.CoreV1().Nodes().Get(ctx, a.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", a.nodeName, err)
	}

	// Merge new labels with existing labels
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	for k, v := range labels {
		node.Labels[k] = v
	}

	_, err = a.kubeClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node %s: %w", a.nodeName, err)
	}

	log.Info("Successfully applied labels to node.")
	return nil
}

// detectGPU populates the label map with GPU-related features.
func (a *Agent) detectGPU(ctx context.Context, labels map[string]string) {
	log := log.FromContext(ctx)

	out, err := a.runCmd(ctx, "nvidia-smi", "--query-gpu=memory.total,compute_cap", "--format=csv,noheader")
	if err == nil {
		a.parseNvidia(string(out), labels)
		return
	}

	out, err = a.runCmd(ctx, "rocm-smi", "--showmeminfo", "vram", "--json")
	if err == nil {
		archOut, _ := a.runCmd(ctx, "rocminfo")
		a.parseRocm(string(out), string(archOut), labels)
		return
	}

	log.Info("no GPU detected")
}

// detectCPU populates the label map with CPU-related features.
func (a *Agent) detectCPU(labels map[string]string) {
	labels[a.labelPrefix+"cpu.avx512"] = strconv.FormatBool(cpu.X86.HasAVX512)
}

// parseNvidia parses output from nvidia-smi and fills the GPU labels.
func (a *Agent) parseNvidia(out string, labels map[string]string) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || len(strings.TrimSpace(lines[0])) == 0 {
		return
	}

	memLine := strings.Split(lines[0], ",")
	if len(memLine) < 2 {
		return
	}

	memFields := strings.Fields(strings.TrimSpace(memLine[0]))
	if len(memFields) > 0 {
		if miB, err := strconv.Atoi(memFields[0]); err == nil {
			labels[a.labelPrefix+"gpu.vram"] = fmt.Sprintf("%dGi", miB/1024)
		}
	}

	cc := strings.TrimSpace(memLine[1])
	ccParts := strings.Split(cc, ".")
	arch := ""
	if len(ccParts) >= 2 {
		arch = fmt.Sprintf("sm_%s%s", ccParts[0], ccParts[1])
	}
	labels[a.labelPrefix+"gpu.arch"] = arch
	if major, err := strconv.Atoi(ccParts[0]); err == nil && major >= 8 {
		labels[a.labelPrefix+"gpu.int4"] = "true"
	} else {
		labels[a.labelPrefix+"gpu.int4"] = "false"
	}

	labels[a.labelPrefix+"gpu.vendor"] = "NVIDIA"
	labels[a.labelPrefix+"gpu.count"] = strconv.Itoa(len(lines))
}

// parseRocm parses output from rocm-smi and rocminfo to fill GPU labels.
func (a *Agent) parseRocm(smiOut, infoOut string, labels map[string]string) {
	var data map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(smiOut), &data); err != nil {
		return
	}
	labels[a.labelPrefix+"gpu.vendor"] = "AMD"
	labels[a.labelPrefix+"gpu.count"] = strconv.Itoa(len(data))

	for _, v := range data {
		for key, val := range v {
			k := strings.ToLower(key)
			if strings.Contains(k, "vram") && strings.Contains(k, "total") {
				str := fmt.Sprint(val)
				num, _ := strconv.Atoi(strings.Fields(str)[0])
				// value may be in MiB or bytes; assume MiB if < 1e7
				if num > 1<<30 {
					num = num / (1024 * 1024)
				}
				labels[a.labelPrefix+"gpu.vram"] = fmt.Sprintf("%dGi", num/1024)
				break
			}
		}
		break
	}

	re := regexp.MustCompile(`gfx[0-9a-z]+`)
	if match := re.FindString(infoOut); match != "" {
		labels[a.labelPrefix+"gpu.arch"] = match
	}

	labels[a.labelPrefix+"gpu.int4"] = "true"
}
