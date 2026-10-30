/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

// The 2026-10-30 gfx1100 placement layout
// (.loom/10-research-voice-stack-and-gpu-placement-2026-10-30.md §6):
// cblevins-5930k always holds the workhorse; cblevins-7900xtx is the flex
// card, with the Mills warm lane as its idle tenant and the creative lanes on
// demand above it. These tests run the shared-group election over the real
// deploy/models manifests, so a later priority or warm-policy edit that
// breaks the layout fails here instead of on the cards.

const (
	layoutGFX1100BudgetMB = 24576 // deploy/gpuprofiles/gfx1100.yaml vramMB
	layoutWorkhorse       = "qwen38-27b-autoround-workhorse"
	layoutWarmLane        = "qwen38-27b-xtx-warm-canary"
)

var layout5930kMembers = []string{
	layoutWorkhorse,
	"qwen36-27b-autoround-workhorse",
	"qwen36-27b-fable-gptq",
	"qwen35-35b-clean-gptq-workhorse",
	"muse-glimmer-30b-tools",
	"qwen35-9b-ablit-rp-5930k",
	"qwen36-35b-mtp-uncensored-5930k",
}

var layoutXTXCreative = []string{
	"qwen-image-21-gfx1100",
	"wan21-t2v-1p3b-gfx1100",
	"wan21-vace-1p3b-i2v-gfx1100",
	"stable-audio-open-gfx1100",
}

// layoutXTXParked are the XTX members that must stay below the idle tenant:
// a demand on any of them preempting the 23 GB warm lane would cost ~3 min of
// swap per TTS decode or probe.
var layoutXTXParked = []string{
	"snac-decode-gfx1100",
	"orpheus-3b-7900xtx",
	"whisper-large-v3-turbo",
	"qwen35-35b-clean-gptq-workhorse-128k",
	"qwen35-35b-clean-mtp-32k",
}

func loadLayoutGroup(t *testing.T, names ...string) map[string]*aiv1alpha2.Model {
	t.Helper()
	out := make(map[string]*aiv1alpha2.Model, len(names))
	for _, name := range names {
		out[name] = loadDeployModelManifest(t, filepath.Join("..", "deploy", "models", name+".yaml"))
	}
	return out
}

func layoutMembers(models map[string]*aiv1alpha2.Model) []*aiv1alpha2.Model {
	out := make([]*aiv1alpha2.Model, 0, len(models))
	for _, m := range models {
		out = append(out, m)
	}
	return out
}

func layoutReady(m *aiv1alpha2.Model, lastDemand *time.Time) {
	m.Status.Phase = aiv1alpha2.ModelPhaseReady
	m.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{GroupName: m.Spec.GPU.Shared, State: "Active"}
	if lastDemand != nil {
		m.Status.LastActiveTime = &metav1.Time{Time: *lastDemand}
	}
}

func layoutDemanded(m *aiv1alpha2.Model, at time.Time) {
	m.Status.Phase = aiv1alpha2.ModelPhasePreempted
	m.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{GroupName: m.Spec.GPU.Shared, State: "Queued"}
	m.Status.LastActiveTime = &metav1.Time{Time: at}
}

func layoutNames(models []*aiv1alpha2.Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.Name)
	}
	return out
}

func TestPlacementLayoutManifestsMatchTheirCards(t *testing.T) {
	cases := map[string]struct {
		group, host string
		names       []string
	}{
		"5930k": {"5930k-textgen", "cblevins-5930k", layout5930kMembers},
		"xtx": {"7900xtx-textgen", "cblevins-7900xtx",
			append(append([]string{layoutWarmLane, "qwen35-9b-ablit-rp"}, layoutXTXCreative...), layoutXTXParked...)},
	}
	for card, tc := range cases {
		for name, m := range loadLayoutGroup(t, tc.names...) {
			if got := m.Spec.GPU.Shared; got != tc.group {
				t.Errorf("%s/%s: gpu.shared = %q, want %q", card, name, got, tc.group)
			}
			if got := m.Spec.NodeSelector["kubernetes.io/hostname"]; got != tc.host {
				t.Errorf("%s/%s: nodeSelector hostname = %q, want %q", card, name, got, tc.host)
			}
			// The XTX advertises amd.com/gpu=2 (dGPU + Raphael iGPU): a dedicated
			// pod that claims one slot can be handed the iGPU.
			if card == "xtx" && m.Spec.GetConfigMap()["dedicatedDeployment"] == true {
				if m.Spec.GPU.Count == nil || *m.Spec.GPU.Count != 2 {
					t.Errorf("xtx/%s: dedicated lane must claim gpu.count 2", name)
				}
			}
		}
	}
}

func TestPlacementLayout5930kWorkhorseIsNeverPreempted(t *testing.T) {
	now := time.Now()
	group := loadLayoutGroup(t, layout5930kMembers...)
	workhorse := group[layoutWorkhorse]
	if !isWarmPinnedSharedModel(workhorse) || !isWarmPrimaryModel(workhorse) {
		t.Fatalf("%s must be minReplicas>=1 and warmPolicy primary", layoutWorkhorse)
	}
	for name := range group {
		if name == layoutWorkhorse {
			continue
		}
		t.Run(name, func(t *testing.T) {
			group := loadLayoutGroup(t, layout5930kMembers...)
			layoutReady(group[layoutWorkhorse], nil)
			layoutDemanded(group[name], now)
			members := layoutMembers(group)

			active := chooseSharedGroupLeaders(members, now, true, layoutGFX1100BudgetMB, false)
			if got := layoutNames(active); len(got) != 1 || got[0] != layoutWorkhorse {
				t.Fatalf("demand on %s changed the Active set to %v, want [%s]", name, got, layoutWorkhorse)
			}
			// Statically parked: the proxy fast-fails instead of starting a
			// cold load the election would kill.
			if !staticallyParkedInGroup(group[name], active[0], active, true, layoutGFX1100BudgetMB) {
				t.Fatalf("%s is not statically parked behind %s", name, layoutWorkhorse)
			}
		})
	}
}

func TestPlacementLayoutXTXCreativeTakesTheCardOnDemand(t *testing.T) {
	now := time.Now()
	for _, name := range layoutXTXCreative {
		t.Run(name, func(t *testing.T) {
			group := loadLayoutGroup(t, append([]string{layoutWarmLane}, layoutXTXCreative...)...)
			layoutReady(group[layoutWarmLane], nil)
			layoutDemanded(group[name], now)
			members := layoutMembers(group)

			if staticallyParkedInGroup(group[name], group[layoutWarmLane], []*aiv1alpha2.Model{group[layoutWarmLane]}, true, layoutGFX1100BudgetMB) {
				t.Fatalf("%s is statically parked behind the idle tenant", name)
			}
			active := chooseSharedGroupLeaders(members, now, true, layoutGFX1100BudgetMB, false)
			if len(active) == 0 || active[0].Name != name {
				t.Fatalf("demand on %s elected %v, want it first", name, layoutNames(active))
			}
		})
	}
}

func TestPlacementLayoutXTXParkedLanesNeverPreemptTheWarmLane(t *testing.T) {
	now := time.Now()
	for _, name := range layoutXTXParked {
		t.Run(name, func(t *testing.T) {
			group := loadLayoutGroup(t, append([]string{layoutWarmLane}, layoutXTXParked...)...)
			layoutReady(group[layoutWarmLane], nil)
			layoutDemanded(group[name], now)

			active := chooseSharedGroupLeaders(layoutMembers(group), now, true, layoutGFX1100BudgetMB, false)
			if got := layoutNames(active); len(got) != 1 || got[0] != layoutWarmLane {
				t.Fatalf("demand on %s changed the Active set to %v, want [%s]", name, got, layoutWarmLane)
			}
		})
	}
}

func TestPlacementLayoutXTXCreativeLanesQueueFirstComeFirstServed(t *testing.T) {
	now := time.Now()
	load := func() map[string]*aiv1alpha2.Model {
		return loadLayoutGroup(t, append([]string{layoutWarmLane}, layoutXTXCreative...)...)
	}
	const holder, next = "qwen-image-21-gfx1100", "wan21-vace-1p3b-i2v-gfx1100"

	// Holder busy (in-flight requests keep its demand fresh): no mid-render kill.
	busy := load()
	last := now.Add(-30 * time.Second)
	layoutReady(busy[holder], &last)
	layoutDemanded(busy[next], now)
	busy[layoutWarmLane].Status.Phase = aiv1alpha2.ModelPhasePreempted
	if got := chooseSharedGroupLeaders(layoutMembers(busy), now, true, layoutGFX1100BudgetMB, false); got[0].Name != holder {
		t.Fatalf("busy %s lost the card to %s: %v", holder, next, layoutNames(got))
	}

	// Holder idle past the 2 min demand window: the queued lane gets the card.
	idle := load()
	last = now.Add(-3 * time.Minute)
	layoutReady(idle[holder], &last)
	layoutDemanded(idle[next], now)
	idle[layoutWarmLane].Status.Phase = aiv1alpha2.ModelPhasePreempted
	if got := chooseSharedGroupLeaders(layoutMembers(idle), now, true, layoutGFX1100BudgetMB, false); got[0].Name != next {
		t.Fatalf("idle %s kept the card from %s: %v", holder, next, layoutNames(got))
	}
}

func TestPlacementLayoutXTXWarmLaneReturnsWhenCreativeIdlesOut(t *testing.T) {
	now := time.Now()
	const creative = "qwen-image-21-gfx1100"
	load := func() map[string]*aiv1alpha2.Model {
		return loadLayoutGroup(t, layoutWarmLane, creative, "snac-decode-gfx1100")
	}

	// Creative Ready and quiet for 3 min, Mills demand on the warm lane: an
	// ondemand idle tenant does not reclaim inside the creative idleTimeout.
	held := load()
	last := now.Add(-3 * time.Minute)
	layoutReady(held[creative], &last)
	layoutDemanded(held[layoutWarmLane], now)
	if got := chooseSharedGroupLeaders(layoutMembers(held), now, true, layoutGFX1100BudgetMB, false); got[0].Name != creative {
		t.Fatalf("warm lane reclaimed inside the creative idle window: %v", layoutNames(got))
	}

	// Creative scaled to zero after its idleTimeout: the warm-pinned tenant wins
	// with or without its own demand.
	for _, warmDemand := range []bool{false, true} {
		back := load()
		back[creative].Status.Phase = aiv1alpha2.ModelPhaseIdle
		back[creative].Status.LastActiveTime = &metav1.Time{Time: now.Add(-20 * time.Minute)}
		back[layoutWarmLane].Status.Phase = aiv1alpha2.ModelPhasePreempted
		if warmDemand {
			back[layoutWarmLane].Status.LastActiveTime = &metav1.Time{Time: now}
		}
		if got := chooseSharedGroupLeaders(layoutMembers(back), now, true, layoutGFX1100BudgetMB, false); got[0].Name != layoutWarmLane {
			t.Fatalf("warmDemand=%v: idle tenant did not return: %v", warmDemand, layoutNames(got))
		}
	}

	// A creative lane leaves room for SNAC decode beside it (multiModel).
	shared := load()
	last = now.Add(-10 * time.Second)
	layoutReady(shared[creative], &last)
	layoutDemanded(shared["snac-decode-gfx1100"], now)
	shared[layoutWarmLane].Status.Phase = aiv1alpha2.ModelPhasePreempted
	got := layoutNames(chooseSharedGroupLeaders(layoutMembers(shared), now, true, layoutGFX1100BudgetMB, false))
	if len(got) != 2 || got[0] != creative || got[1] != "snac-decode-gfx1100" {
		t.Fatalf("snac should co-reside with %s, got %v", creative, got)
	}
}

func TestPlacementLayoutWorkhorseLabelsFailOverAcrossCards(t *testing.T) {
	lanes := loadLayoutGroup(t, layoutWorkhorse, layoutWarmLane)
	shared := map[string]bool{}
	for _, l := range lanes[layoutWorkhorse].Spec.ServiceLabels {
		shared[l] = true
	}
	for _, want := range []string{"workhorse", "loom-workhorse", "reasoning", "qwen38-27b"} {
		if !shared[want] {
			t.Fatalf("%s lost serviceLabel %q", layoutWorkhorse, want)
		}
		found := false
		for _, l := range lanes[layoutWarmLane].Spec.ServiceLabels {
			found = found || l == want
		}
		if !found {
			t.Errorf("%s must also claim %q so the proxy can fail the label over", layoutWarmLane, want)
		}
	}
	// The aliases stay single-claimant: the proxy alias cache and LiteLLM keep
	// resolving them to the 5930k lane.
	for _, a := range lanes[layoutWarmLane].Spec.LiteLLM.Aliases {
		if shared[a] {
			t.Errorf("%s must not take litellm alias %q (alias claims are first-wins)", layoutWarmLane, a)
		}
	}
	whPrio := modelRoutePriorityForTest(lanes[layoutWorkhorse])
	warmPrio := modelRoutePriorityForTest(lanes[layoutWarmLane])
	if whPrio <= warmPrio {
		t.Fatalf("route-priority: %s=%d must exceed %s=%d so the 5930k lane is preferred while Ready",
			layoutWorkhorse, whPrio, layoutWarmLane, warmPrio)
	}
}

func modelRoutePriorityForTest(m *aiv1alpha2.Model) int {
	raw := m.Annotations[constants.AnnotationRoutePriority]
	if raw == "" {
		return 0
	}
	var v int
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return 0
	}
	return v
}

// TestPlacementLayoutDoesNotRestartTheWarmLanes proves the layout edits to the
// two Ready 27B lanes (priority, minReplicas, warmPolicy, serviceLabels,
// route-priority) leave their pod templates byte-identical, so applying the
// layout does not restart either lane.
func TestPlacementLayoutDoesNotRestartTheWarmLanes(t *testing.T) {
	cases := map[string]struct {
		priority    int32
		minReplicas int32
		warmPolicy  string
		labels      []string
	}{
		layoutWorkhorse: {500, 0, "ondemand", []string{"qwen38-27b", "workhorse", "loom-workhorse", "reasoning"}},
		layoutWarmLane:  {470, 1, "primary", []string{"mills-warm", "mills-council", "xtx-warm"}},
	}
	for name, before := range cases {
		t.Run(name, func(t *testing.T) {
			after := loadDeployModelManifest(t, filepath.Join("..", "deploy", "models", name+".yaml"))
			prior := after.DeepCopy()
			prior.Spec.GPU.Priority = &before.priority
			prior.Spec.Serverless.MinReplicas = &before.minReplicas
			prior.Spec.ServiceLabels = before.labels
			delete(prior.Annotations, constants.AnnotationRoutePriority)
			cfg := map[string]any{}
			for k, v := range after.Spec.GetConfigMap() {
				cfg[k] = v
			}
			cfg["warmPolicy"] = before.warmPolicy
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("encode config: %v", err)
			}
			prior.Spec.Config = &apiextensionsv1.JSON{Raw: raw}

			got := renderDedicatedFlagDeployment(t, after)
			want := renderDedicatedFlagDeployment(t, prior)
			if !equality.Semantic.DeepEqual(got.Spec.Template, want.Spec.Template) {
				t.Fatalf("layout edit changed the pod template of %s:\nafter:  %s\nbefore: %s",
					name, dedicatedFlagJSON(t, got.Spec.Template), dedicatedFlagJSON(t, want.Spec.Template))
			}
		})
	}
}
