package agentcontext

import "sort"

// AgentCandidate is an active agent considered for fleet task dispatch.
// Capabilities is the union of capability tags the agent advertises.
// Load is an integer cost metric (e.g. in-flight task count) — lower is better.
type AgentCandidate struct {
	AgentID      string
	Capabilities []string
	Load         int
}

// ChooseAgent picks the best candidate for a task using a pure-function scorer.
//
// Rules:
//   - Candidates missing any entry in task.CapabilityNeeded are skipped.
//   - Among surviving candidates, lowest Load wins.
//   - Ties are broken by deterministic ascending sort on AgentID.
//   - Empty task.CapabilityNeeded: all candidates survive the capability check.
//   - Empty candidate set (or no survivors): returns ("", "no_candidates"
//     or "no_capability_match").
//
// Return reasons: "chosen", "no_candidates", "no_capability_match".
func ChooseAgent(task Task, candidates []AgentCandidate) (string, string) {
	if len(candidates) == 0 {
		return "", "no_candidates"
	}

	required := task.CapabilityNeeded
	eligible := make([]AgentCandidate, 0, len(candidates))
	for _, c := range candidates {
		if hasAllCapabilities(c.Capabilities, required) {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		return "", "no_capability_match"
	}

	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].Load != eligible[j].Load {
			return eligible[i].Load < eligible[j].Load
		}
		return eligible[i].AgentID < eligible[j].AgentID
	})

	return eligible[0].AgentID, "chosen"
}

// hasAllCapabilities returns true when every element of required appears in have.
// Empty required → true.
func hasAllCapabilities(have, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(have))
	for _, c := range have {
		set[c] = struct{}{}
	}
	for _, r := range required {
		if _, ok := set[r]; !ok {
			return false
		}
	}
	return true
}
