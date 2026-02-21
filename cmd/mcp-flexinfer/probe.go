package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *flexinferServer) handleProbe(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	report := map[string]any{
		"ok":        true,
		"namespace": namespace,
	}

	// Kubeconfig check.
	kubeInfo := map[string]any{"path": f.kubeconfig}
	if _, err := f.kubeDynamicClient(); err != nil {
		kubeInfo["ok"] = false
		kubeInfo["error"] = err.Error()
	} else {
		kubeInfo["ok"] = true
	}
	report["kubeconfig"] = kubeInfo

	// CRD detection.
	type crdProbe struct {
		Name string
		GVRs []schema.GroupVersionResource
	}
	probes := []crdProbe{
		{Name: "models", GVRs: modelGVRs},
		{Name: "loraadapters", GVRs: loraGVRs},
		{Name: "modelcatalogs", GVRs: catalogGVRs},
	}

	crdDetected := make([]any, 0)
	crdMissing := make([]string, 0)
	for _, p := range probes {
		list, gvr, err := f.listUnstructuredWithFallback(ctx, p.GVRs, namespace, false)
		if err != nil {
			crdMissing = append(crdMissing, p.Name)
			continue
		}
		crdDetected = append(crdDetected, map[string]any{
			"name":  p.Name,
			"gvr":   gvr.String(),
			"count": len(list.Items),
		})
	}
	report["crds"] = map[string]any{
		"detected": crdDetected,
		"missing":  crdMissing,
	}

	// Controller and proxy pod checks.
	controllers := []string{"flexinfer-controller-manager", "flexinfer-proxy"}
	ctrlCounts := make(map[string]any)
	if cs, err := f.kubeClientset(); err == nil {
		for _, c := range controllers {
			pods, _ := listFlexinferPods(ctx, cs, namespace, c)
			entry := map[string]any{"count": len(pods)}
			if len(pods) > 0 {
				entry["examples"] = pods[:min(len(pods), 3)]
			}
			ctrlCounts[c] = entry
		}
	} else {
		report["controllers_error"] = err.Error()
	}
	report["controllers"] = ctrlCounts

	// GPU node count.
	gpuCount := 0
	if cs, err := f.kubeClientset(); err == nil {
		nodes, nerr := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if nerr == nil {
			for _, node := range nodes.Items {
				hasGPU := false
				for k := range node.Labels {
					if strings.HasPrefix(k, "flexinfer.ai/gpu") || strings.HasPrefix(k, "node.flexstack.io/gpu") {
						hasGPU = true
						break
					}
				}
				if !hasGPU {
					for resName := range node.Status.Capacity {
						rn := string(resName)
						if strings.Contains(rn, "nvidia.com/gpu") || strings.Contains(rn, "amd.com/gpu") {
							hasGPU = true
							break
						}
					}
				}
				if hasGPU {
					gpuCount++
				}
			}
		}
	}
	report["gpu_node_count"] = gpuCount

	// Proxy URL status.
	report["proxy_url"] = f.proxyURL
	if f.proxyURL != "" {
		report["proxy_configured"] = true
	} else {
		report["proxy_configured"] = false
	}

	// Guidance.
	guidance := make([]string, 0)
	if ok, _ := kubeInfo["ok"].(bool); !ok {
		guidance = append(guidance, "Set KUBECONFIG or FLEXINFER_KUBECONFIG to a valid kubeconfig path")
	}
	if len(crdDetected) == 0 {
		guidance = append(guidance, "FlexInfer CRDs not detected; verify installation and namespace")
	}
	if f.proxyURL == "" {
		guidance = append(guidance, "Set FLEXINFER_PROXY_URL for proxy_models and proxy_health tools")
	}
	report["guidance"] = guidance

	return mcp.JSONResult(report)
}

func listFlexinferPods(ctx context.Context, cs kubernetes.Interface, namespace, component string) ([]string, error) {
	selectors := []string{
		fmt.Sprintf("app.kubernetes.io/name=%s", component),
		fmt.Sprintf("app=%s", component),
		fmt.Sprintf("control-plane=%s", component),
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var lastErr error
	for _, sel := range selectors {
		pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
		if err != nil {
			lastErr = err
			continue
		}
		if len(pods.Items) == 0 {
			continue
		}

		out := make([]string, 0, len(pods.Items))
		for _, p := range pods.Items {
			out = append(out, p.Name)
		}
		return out, nil
	}

	return nil, lastErr
}

// listResourceEvents fetches K8s events for a specific resource.
func listResourceEvents(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string) []map[string]any {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	evs, err := cs.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.kind=%s,involvedObject.name=%s", kind, name),
	})
	if err != nil || len(evs.Items) == 0 {
		return nil
	}

	events := make([]map[string]any, 0, len(evs.Items))
	for _, e := range evs.Items[:min(len(evs.Items), 20)] {
		events = append(events, map[string]any{
			"type":    e.Type,
			"reason":  e.Reason,
			"message": e.Message,
			"count":   e.Count,
			"last":    e.LastTimestamp.Format("2006-01-02T15:04:05Z"),
		})
	}
	return events
}
