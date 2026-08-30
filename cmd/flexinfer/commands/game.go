package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/gaming"
)

// currentUserFn is a DI point so tests can pin the default --owner.
var currentUserFn = func() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

var gameCmd = &cobra.Command{
	Use:   "game",
	Short: "GPU gaming leases (Sunshine/Moonlight) on FlexInfer nodes",
	Long: `Manage ephemeral GPU gaming leases.

A gaming lease is a GamingSession object: creating one drains the node's
inference workloads (the scheduling overlay parks them; nothing in any model
manifest is edited) and starts the Sunshine streaming host once the node is
clear and the runtime image passes the GitOps-owned gaming policy. Deleting
the session — or its TTL/idle expiry — restores inference automatically.

Examples:
  # Check whether a node could game right now (no mutation)
  flexinfer game preflight cblevins-7900xtx

  # Start a 2-hour session and wait for Sunshine
  flexinfer game start cblevins-7900xtx --for 2h --wait

  # See sessions and their conditions
  flexinfer game status

  # End the session and restore inference
  flexinfer game stop cblevins-7900xtx`,
}

var (
	gameStartFor         time.Duration
	gameStartOwner       string
	gameStartIdleTimeout time.Duration
	gameStartWait        bool
	gameStopWait         bool
)

var gameStartCmd = &cobra.Command{
	Use:   "start <node>",
	Short: "Start a gaming lease on a node",
	Args:  cobra.ExactArgs(1),
	RunE:  runGameStart,
}

var gameStopCmd = &cobra.Command{
	Use:   "stop <node>",
	Short: "End the node's gaming lease and restore inference",
	Args:  cobra.ExactArgs(1),
	RunE:  runGameStop,
}

var gameStatusCmd = &cobra.Command{
	Use:   "status [node]",
	Short: "Show gaming sessions and their conditions",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runGameStatus,
}

var gamePreflightCmd = &cobra.Command{
	Use:   "preflight <node>",
	Short: "Check whether a node can host a gaming session (read-only)",
	Args:  cobra.ExactArgs(1),
	RunE:  runGamePreflight,
}

func init() {
	gameStartCmd.Flags().DurationVar(&gameStartFor, "for", 0, "session TTL (e.g. 2h); defaults to the gaming policy's defaultTTL")
	gameStartCmd.Flags().StringVar(&gameStartOwner, "owner", "", "who owns the session (defaults to the local username)")
	gameStartCmd.Flags().DurationVar(&gameStartIdleTimeout, "idle-timeout", 0, "end the session after this long with no Moonlight client (requires runtime client telemetry)")
	gameStartCmd.Flags().BoolVar(&gameStartWait, "wait", false, "wait until Sunshine is ready (or the session is refused)")
	gameStopCmd.Flags().BoolVar(&gameStopWait, "wait", false, "wait until the session is fully reverted and gone")

	gameCmd.AddCommand(gameStartCmd)
	gameCmd.AddCommand(gameStopCmd)
	gameCmd.AddCommand(gameStatusCmd)
	gameCmd.AddCommand(gamePreflightCmd)
}

func gamingSessionName(node string) string {
	return "gaming-" + node
}

func runGameStart(cmd *cobra.Command, args []string) error {
	node := args[0]
	out := cmd.OutOrStdout()

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	c := ctx()

	// Refuse to double-book the node.
	existing := &aiv1alpha2.GamingSessionList{}
	if err := k8sClient.List(c, existing, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("listing gaming sessions: %w", err)
	}
	for i := range existing.Items {
		s := &existing.Items[i]
		if s.Spec.NodeName == node && s.DeletionTimestamp.IsZero() && s.Status.Phase != aiv1alpha2.GamingSessionExpired {
			return fmt.Errorf("node %s already has gaming session %q (phase %s) — use `flexinfer game status %s` or `flexinfer game stop %s`",
				node, s.Name, s.Status.Phase, node, node)
		}
	}

	owner := gameStartOwner
	if owner == "" {
		owner = currentUserFn()
	}

	gs := &aiv1alpha2.GamingSession{
		ObjectMeta: metav1.ObjectMeta{Name: gamingSessionName(node), Namespace: namespace},
		Spec: aiv1alpha2.GamingSessionSpec{
			NodeName: node,
			Mode:     "gaming",
			Owner:    owner,
		},
	}
	if gameStartFor > 0 {
		gs.Spec.TTL = &metav1.Duration{Duration: gameStartFor}
	}
	if gameStartIdleTimeout > 0 {
		gs.Spec.IdleTimeout = &metav1.Duration{Duration: gameStartIdleTimeout}
	}

	if err := k8sClient.Create(c, gs); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("gaming session %q already exists — a concluded session must be deleted first: `flexinfer game stop %s`", gs.Name, node)
		}
		return fmt.Errorf("creating gaming session: %w", err)
	}
	ttlNote := "policy default TTL"
	if gameStartFor > 0 {
		ttlNote = gameStartFor.String()
	}
	_, _ = fmt.Fprintf(out, "GamingSession %q created for node %s (owner %s, ttl %s)\n", gs.Name, node, owner, ttlNote)

	if !gameStartWait {
		_, _ = fmt.Fprintf(out, "Watch progress with: flexinfer game status %s\n", node)
		return nil
	}
	return waitForGamingSession(c, k8sClient, out, gs.Name)
}

// waitForGamingSession polls the session until Sunshine is ready, the session
// is refused/failed, or the wait times out. Prints phase transitions.
func waitForGamingSession(c context.Context, k8sClient client.Client, out io.Writer, name string) error {
	const (
		pollInterval = 2 * time.Second
		waitTimeout  = 15 * time.Minute
	)
	deadline := time.Now().Add(waitTimeout)
	lastMsg := ""
	for {
		if c.Err() != nil {
			return c.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for gaming session %q", waitTimeout, name)
		}
		gs := &aiv1alpha2.GamingSession{}
		if err := k8sClient.Get(c, types.NamespacedName{Name: name, Namespace: namespace}, gs); err != nil {
			return fmt.Errorf("waiting on gaming session: %w", err)
		}
		if msg := fmt.Sprintf("%s: %s", gs.Status.Phase, gs.Status.Message); msg != lastMsg && gs.Status.Phase != "" {
			_, _ = fmt.Fprintln(out, msg)
			lastMsg = msg
		}
		if cond := apimeta.FindStatusCondition(gs.Status.Conditions, aiv1alpha2.GamingConditionCapabilityReady); cond != nil && cond.Status == metav1.ConditionFalse {
			return fmt.Errorf("session refused (%s): %s", cond.Reason, cond.Message)
		}
		switch gs.Status.Phase {
		case aiv1alpha2.GamingSessionActive:
			_, _ = fmt.Fprintln(out, "Sunshine is up — pair/connect Moonlight from the Steam Deck.")
			return nil
		case aiv1alpha2.GamingSessionExpired:
			return fmt.Errorf("session ended before activation (%s): %s", gs.Status.EndReason, gs.Status.Message)
		}
		select {
		case <-c.Done():
			return c.Err()
		case <-time.After(pollInterval):
		}
	}
}

func runGameStop(cmd *cobra.Command, args []string) error {
	node := args[0]
	out := cmd.OutOrStdout()

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	c := ctx()

	sessions := &aiv1alpha2.GamingSessionList{}
	if err := k8sClient.List(c, sessions, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("listing gaming sessions: %w", err)
	}
	var targets []*aiv1alpha2.GamingSession
	for i := range sessions.Items {
		s := &sessions.Items[i]
		if s.Spec.NodeName == node || s.Name == node {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("no gaming session found for node %q in namespace %s", node, namespace)
	}
	for _, s := range targets {
		if !s.DeletionTimestamp.IsZero() {
			_, _ = fmt.Fprintf(out, "GamingSession %q already deleting\n", s.Name)
			continue
		}
		if err := k8sClient.Delete(c, s); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting gaming session %q: %w", s.Name, err)
		}
		_, _ = fmt.Fprintf(out, "GamingSession %q deleted — node reverts to inference and parked models restore\n", s.Name)
	}
	if !gameStopWait {
		return nil
	}
	const (
		pollInterval = 2 * time.Second
		waitTimeout  = 5 * time.Minute
	)
	deadline := time.Now().Add(waitTimeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for session finalization (mode revert may still be in progress)")
		}
		gone := true
		for _, s := range targets {
			check := &aiv1alpha2.GamingSession{}
			err := k8sClient.Get(c, types.NamespacedName{Name: s.Name, Namespace: namespace}, check)
			if err == nil {
				gone = false
			} else if !apierrors.IsNotFound(err) {
				return fmt.Errorf("waiting for session deletion: %w", err)
			}
		}
		if gone {
			_, _ = fmt.Fprintln(out, "Session finalized; node is back on inference duty.")
			return nil
		}
		select {
		case <-c.Done():
			return c.Err()
		case <-time.After(pollInterval):
		}
	}
}

func runGameStatus(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	c := ctx()

	sessions := &aiv1alpha2.GamingSessionList{}
	if err := k8sClient.List(c, sessions, client.InNamespace(getNamespace())); err != nil {
		return fmt.Errorf("listing gaming sessions: %w", err)
	}
	items := sessions.Items
	if len(args) == 1 {
		var filtered []aiv1alpha2.GamingSession
		for _, s := range items {
			if s.Spec.NodeName == args[0] || s.Name == args[0] {
				filtered = append(filtered, s)
			}
		}
		items = filtered
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "No gaming sessions found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tNODE\tPHASE\tOWNER\tEXPIRES\tSTREAM\tMESSAGE")
	for i := range items {
		s := &items[i]
		expires := "-"
		if s.Status.ExpiresAt != nil {
			expires = time.Until(s.Status.ExpiresAt.Time).Round(time.Second).String()
			if s.Status.ExpiresAt.Time.Before(time.Now()) {
				expires = "expired"
			}
		}
		stream := conditionShort(s.Status.Conditions, aiv1alpha2.GamingConditionStreamActive)
		phase := string(s.Status.Phase)
		if s.Status.EndReason != "" && s.Status.Phase == aiv1alpha2.GamingSessionExpired {
			phase = phase + "/" + s.Status.EndReason
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Name, s.Spec.NodeName, phase, s.Spec.Owner, expires, stream, truncate(s.Status.Message, 72))
	}
	_ = w.Flush()

	// Single-session detail: full condition table.
	if len(args) == 1 && len(items) == 1 {
		s := &items[0]
		_, _ = fmt.Fprintln(out, "\nConditions:")
		cw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(cw, "  TYPE\tSTATUS\tREASON\tMESSAGE")
		for _, cond := range s.Status.Conditions {
			_, _ = fmt.Fprintf(cw, "  %s\t%s\t%s\t%s\n", cond.Type, cond.Status, cond.Reason, truncate(cond.Message, 90))
		}
		_ = cw.Flush()
	}
	return nil
}

func conditionShort(conds []metav1.Condition, condType string) string {
	cond := apimeta.FindStatusCondition(conds, condType)
	if cond == nil {
		return "-"
	}
	switch cond.Status {
	case metav1.ConditionTrue:
		return "yes"
	case metav1.ConditionFalse:
		return "no"
	default:
		return "unknown"
	}
}

// preflight checks are read-only and mirror the controller's activation gates
// plus the operational drain/label-coverage surface.
func runGamePreflight(cmd *cobra.Command, args []string) error {
	node := args[0]
	out := cmd.OutOrStdout()

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	c := ctx()
	failures := 0
	pass := func(msg string) { _, _ = fmt.Fprintln(out, "  ✓ "+msg) }
	warn := func(msg string) { _, _ = fmt.Fprintln(out, "  ! "+msg) }
	fail := func(msg string) { failures++; _, _ = fmt.Fprintln(out, "  ✗ "+msg) }

	_, _ = fmt.Fprintf(out, "Gaming preflight for node %s (namespace %s)\n\n", node, namespace)

	// 1. Existing session on the node.
	sessions := &aiv1alpha2.GamingSessionList{}
	if err := k8sClient.List(c, sessions, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("listing gaming sessions: %w", err)
	}
	blocked := false
	for i := range sessions.Items {
		s := &sessions.Items[i]
		if s.Spec.NodeName == node && s.DeletionTimestamp.IsZero() && s.Status.Phase != aiv1alpha2.GamingSessionExpired {
			fail(fmt.Sprintf("node already has gaming session %q (phase %s)", s.Name, s.Status.Phase))
			blocked = true
		}
	}
	if !blocked {
		pass("no existing gaming session on the node")
	}

	// 2. Gaming policy.
	policy := gaming.Default()
	policyCM := &corev1.ConfigMap{}
	err = k8sClient.Get(c, types.NamespacedName{Name: gaming.PolicyConfigMapName, Namespace: namespace}, policyCM)
	switch {
	case err == nil:
		policy = gaming.ParsePolicy(policyCM.Data)
		pass(fmt.Sprintf("gaming policy %q found (defaultTTL %s, maxTTL %s, drainTimeout %s)", gaming.PolicyConfigMapName, policy.DefaultTTL, policy.MaxTTL, policy.DrainTimeout))
	case apierrors.IsNotFound(err):
		warn(fmt.Sprintf("gaming policy ConfigMap %q not found — controller uses built-in secure defaults", gaming.PolicyConfigMapName))
	default:
		return fmt.Errorf("reading gaming policy: %w", err)
	}
	if len(policy.ApprovedImages) == 0 {
		fail("no approved gaming images in policy — activation will be refused until the streaming/security canary passes and its digest is added to gaming.approvedImages (docs/dev/gaming-image-canary.md)")
	} else {
		pass(fmt.Sprintf("%d approved gaming image(s) in policy", len(policy.ApprovedImages)))
	}

	// 3. Runtime pod on the node + image approval + hostNetwork.
	pods := &corev1.PodList{}
	if err := k8sClient.List(c, pods, client.InNamespace(namespace),
		client.MatchingLabels{"app.kubernetes.io/component": "flexinfer-runtime"}); err != nil {
		return fmt.Errorf("listing runtime pods: %w", err)
	}
	var runtimePod *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName == node || (p.Spec.NodeName == "" && p.Spec.NodeSelector["kubernetes.io/hostname"] == node) {
			runtimePod = p
			break
		}
	}
	if runtimePod == nil {
		fail("no flexinfer runtime pod found on the node")
	} else {
		if runtimePod.Status.Phase == corev1.PodRunning {
			pass(fmt.Sprintf("runtime pod %s is Running", runtimePod.Name))
		} else {
			fail(fmt.Sprintf("runtime pod %s is %s", runtimePod.Name, runtimePod.Status.Phase))
		}
		approved := false
		var images []string
		for i := range runtimePod.Spec.Containers {
			image := runtimePod.Spec.Containers[i].Image
			imageID := ""
			for j := range runtimePod.Status.ContainerStatuses {
				if runtimePod.Status.ContainerStatuses[j].Name == runtimePod.Spec.Containers[i].Name {
					imageID = runtimePod.Status.ContainerStatuses[j].ImageID
				}
			}
			images = append(images, image)
			if policy.ImageApproved(image, imageID) {
				approved = true
			}
		}
		if approved {
			pass("runtime image is on the approved gaming image list")
		} else {
			fail(fmt.Sprintf("runtime image not approved for gaming: %s", strings.Join(images, ", ")))
		}
		if runtimePod.Spec.HostNetwork {
			pass("runtime pod uses hostNetwork (Sunshine reachable on the LAN)")
		} else {
			warn("runtime pod is not hostNetwork — Sunshine would not be reachable by Moonlight clients (gfx1100 profile needs the gaming stanza)")
		}
	}

	// 4. Drain surface + label coverage.
	models := &aiv1alpha2.ModelList{}
	if err := k8sClient.List(c, models, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("listing models: %w", err)
	}
	var pinned []*aiv1alpha2.Model
	labelCarriers := map[string]int{}       // serviceLabel -> carriers anywhere (litellm-enabled)
	labelPinnedCarriers := map[string]int{} // serviceLabel -> carriers pinned to this node
	for i := range models.Items {
		m := &models.Items[i]
		pinnedHere := m.Spec.NodeSelector != nil && m.Spec.NodeSelector["kubernetes.io/hostname"] == node
		if pinnedHere {
			pinned = append(pinned, m)
		}
		if !modelLiteLLMEnabled(m) {
			continue
		}
		for _, l := range m.Spec.ServiceLabels {
			labelCarriers[l]++
			if pinnedHere {
				labelPinnedCarriers[l]++
			}
		}
	}
	if len(pinned) == 0 {
		pass("no models pinned to the node")
	} else {
		var ready []string
		for _, m := range pinned {
			if m.Status.Phase == aiv1alpha2.ModelPhaseReady || m.Status.Phase == aiv1alpha2.ModelPhaseLoading {
				ready = append(ready, fmt.Sprintf("%s(%s)", m.Name, m.Status.Phase))
			}
		}
		_, _ = fmt.Fprintf(out, "  i %d model(s) pinned to the node will be parked for the session", len(pinned))
		if len(ready) > 0 {
			_, _ = fmt.Fprintf(out, "; currently serving: %s", strings.Join(ready, ", "))
		}
		_, _ = fmt.Fprintln(out)
	}
	var darkLabels []string
	for l, pinnedCount := range labelPinnedCarriers {
		if pinnedCount > 0 && labelCarriers[l] == pinnedCount {
			darkLabels = append(darkLabels, l)
		}
	}
	sort.Strings(darkLabels)
	if len(darkLabels) > 0 {
		warn(fmt.Sprintf("service label(s) with no carrier off this node — they go dark during the session: %s", strings.Join(darkLabels, ", ")))
	} else if len(pinned) > 0 {
		pass("every service label carried by the node has a carrier elsewhere")
	}

	_, _ = fmt.Fprintln(out)
	if failures > 0 {
		return fmt.Errorf("preflight failed: %d blocking check(s)", failures)
	}
	_, _ = fmt.Fprintln(out, "Preflight OK — start with: flexinfer game start "+node+" --for 2h")
	return nil
}

func modelLiteLLMEnabled(m *aiv1alpha2.Model) bool {
	if m.Spec.LiteLLM == nil || m.Spec.LiteLLM.Enabled == nil {
		return true
	}
	return *m.Spec.LiteLLM.Enabled
}
