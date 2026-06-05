package benchmarker

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// resolveServingNodeName resolves the cluster node currently serving the model
// being benchmarked. This is the GPU node behind the proxy, NOT the benchmarker's
// own runner node (b.nodeName, from the downward-API NODE_NAME), which is typically
// a CPU worker and carries no GPU device-class labels. Reading device class from the
// runner node is what produced empty rows (vendor=,arch=,vram=,count=,int4=) in the
// Postgres benchmarks table (see #34 / MR !572).
//
// It reads the model's Endpoints (named after the ModelDeployment, in the
// benchmarker's namespace) and resolves the backing node in priority order:
//  1. endpoint address NodeName — populated by the endpoint-controller for
//     dedicated-pod, selector-based Services (e.g. vllm text-gen pods).
//  2. endpoint address TargetRef → Pod → Spec.NodeName.
//  3. endpoint address IP → matching Pod in the namespace → Spec.NodeName.
//     Runtime-served models (multi-subprocess runtime DaemonSet) get
//     manually-managed Endpoints that carry only an IP, so the first two tiers
//     yield nothing and we match the runtime pod by IP.
//
// Returns "" when the node cannot be resolved (model not Ready, Endpoints absent,
// or insufficient RBAC); callers fall back to the runner node.
func (b *Benchmarker) resolveServingNodeName(ctx context.Context) string {
	logger := log.FromContext(ctx)
	if b.kubeClient == nil || b.modelName == "" {
		return ""
	}

	ep, err := b.kubeClient.CoreV1().Endpoints(b.namespace).Get(ctx, b.modelName, metav1.GetOptions{})
	if err != nil {
		logger.V(1).Info("could not get model Endpoints to resolve serving node; falling back to runner node",
			"model", b.modelName, "namespace", b.namespace, "error", err.Error())
		return ""
	}

	for _, subset := range ep.Subsets {
		for _, addr := range subset.Addresses {
			if addr.NodeName != nil && *addr.NodeName != "" {
				return *addr.NodeName
			}
			if addr.TargetRef != nil && addr.TargetRef.Kind == "Pod" && addr.TargetRef.Name != "" {
				podNS := addr.TargetRef.Namespace
				if podNS == "" {
					podNS = b.namespace
				}
				if node := b.nodeNameForPod(ctx, podNS, addr.TargetRef.Name); node != "" {
					return node
				}
			}
			if addr.IP != "" {
				if node := b.nodeNameForPodIP(ctx, b.namespace, addr.IP); node != "" {
					return node
				}
			}
		}
	}

	logger.V(1).Info("model Endpoints carried no resolvable serving node; falling back to runner node",
		"model", b.modelName, "namespace", b.namespace)
	return ""
}

// nodeNameForPod returns the node a named pod is scheduled on, or "" on error.
func (b *Benchmarker) nodeNameForPod(ctx context.Context, namespace, name string) string {
	pod, err := b.kubeClient.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	return pod.Spec.NodeName
}

// nodeNameForPodIP returns the node of the pod whose status PodIP matches ip, or
// "" if none is found. Used for runtime-served models whose Endpoints carry only
// an IP (no NodeName/TargetRef).
func (b *Benchmarker) nodeNameForPodIP(ctx context.Context, namespace, ip string) string {
	pods, err := b.kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ""
	}
	for i := range pods.Items {
		if pods.Items[i].Status.PodIP == ip {
			return pods.Items[i].Spec.NodeName
		}
	}
	return ""
}
