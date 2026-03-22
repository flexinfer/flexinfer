package agent

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/flexinfer/flexinfer/agents/termination"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

// DrainCoordinator watches for spot termination signals and gracefully
// drains GPU workloads from the node before it is reclaimed.
type DrainCoordinator struct {
	kubeClient  kubernetes.Interface
	nodeName    string
	detector    termination.TerminationDetector
	labelPrefix string
}

// NewDrainCoordinator creates a drain coordinator with auto-detected termination detector.
func NewDrainCoordinator(kubeClient kubernetes.Interface, nodeName, labelPrefix string) *DrainCoordinator {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	detector := termination.AutoDetect(ctx)

	return &DrainCoordinator{
		kubeClient:  kubeClient,
		nodeName:    nodeName,
		detector:    detector,
		labelPrefix: labelPrefix,
	}
}

// Run starts watching for termination signals. Blocks until termination is detected
// or the context is cancelled.
func (d *DrainCoordinator) Run(ctx context.Context) error {
	log := log.FromContext(ctx)
	log.Info("Drain coordinator started", "detector", d.detector.Name(), "node", d.nodeName)

	timeRemaining, err := d.detector.Watch(ctx)
	if err != nil {
		return fmt.Errorf("termination detector: %w", err)
	}

	log.Info("Spot termination detected!", "timeRemaining", timeRemaining, "detector", d.detector.Name())

	// Step 1: Taint the node to prevent new scheduling
	if err := d.taintNode(ctx); err != nil {
		log.Error(err, "Failed to taint node, continuing drain anyway")
	}

	// Step 2: Annotate node to signal GPUGroup controller for preemption
	if err := d.annotateTerminating(ctx); err != nil {
		log.Error(err, "Failed to annotate node for termination")
	}

	// Step 3: Emit events on affected model pods
	d.emitEventsOnAffectedPods(ctx)

	log.Info("Drain coordination complete, node marked for termination")
	return nil
}

// taintNode adds the NoSchedule taint to prevent new pods from being scheduled.
func (d *DrainCoordinator) taintNode(ctx context.Context) error {
	node, err := d.kubeClient.CoreV1().Nodes().Get(ctx, d.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get node: %w", err)
	}

	// Check if taint already exists
	for _, t := range node.Spec.Taints {
		if t.Key == constants.TaintKeySpotTerminating {
			return nil // Already tainted
		}
	}

	node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
		Key:    constants.TaintKeySpotTerminating,
		Value:  "true",
		Effect: corev1.TaintEffectNoSchedule,
	})

	_, err = d.kubeClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	return err
}

// annotateTerminating sets an annotation that the proxy and GPUGroup controller can observe.
func (d *DrainCoordinator) annotateTerminating(ctx context.Context) error {
	node, err := d.kubeClient.CoreV1().Nodes().Get(ctx, d.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get node: %w", err)
	}

	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[constants.NodeAnnotationSpotTerminating] = "true"
	node.Annotations[constants.NodeAnnotationSpotTerminatingAt] = time.Now().UTC().Format(time.RFC3339)

	_, err = d.kubeClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	return err
}

// emitEventsOnAffectedPods records events on FlexInfer-managed pods running on this node.
func (d *DrainCoordinator) emitEventsOnAffectedPods(ctx context.Context) {
	log := log.FromContext(ctx)

	pods, err := d.kubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + d.nodeName,
		LabelSelector: "app.kubernetes.io/managed-by=flexinfer",
	})
	if err != nil {
		log.Error(err, "Failed to list pods for drain events")
		return
	}

	for _, pod := range pods.Items {
		_, err := d.kubeClient.CoreV1().Events(pod.Namespace).Create(ctx, &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "spot-termination-",
				Namespace:    pod.Namespace,
			},
			InvolvedObject: corev1.ObjectReference{
				Kind:       "Pod",
				Name:       pod.Name,
				Namespace:  pod.Namespace,
				UID:        pod.UID,
				APIVersion: "v1",
			},
			Reason:  "SpotTermination",
			Message: fmt.Sprintf("Node %s is being reclaimed (spot instance termination)", d.nodeName),
			Type:    corev1.EventTypeWarning,
			Source: corev1.EventSource{
				Component: "flexinfer-agent",
			},
			FirstTimestamp: metav1.Now(),
			LastTimestamp:  metav1.Now(),
		}, metav1.CreateOptions{})
		if err != nil {
			log.V(1).Info("Failed to create event for pod", "pod", pod.Name, "error", err)
		}
	}

	log.Info("Emitted termination events", "podCount", len(pods.Items))
}
