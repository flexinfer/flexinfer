package redact

import "testing"

func TestLookupPolicy_KnownTool(t *testing.T) {
	p := lookupPolicy("Read", TierPublic)
	if p.Args["file_path"].Action != ActionPathOnly {
		t.Errorf("Read.public.file_path: got %v, want %v", p.Args["file_path"].Action, ActionPathOnly)
	}
	if p.Default.Action != ActionDrop {
		t.Errorf("Read.public.default: got %v, want %v", p.Default.Action, ActionDrop)
	}
	if p.Result.Action != ActionSizeOnly {
		t.Errorf("Read.public.result: got %v, want %v", p.Result.Action, ActionSizeOnly)
	}
}

func TestLookupPolicy_UnknownTool_Public_DropsEverything(t *testing.T) {
	p := lookupPolicy("UnknownTool", TierPublic)
	if p.Default.Action != ActionDrop {
		t.Errorf("unknown.public.default: got %v, want %v", p.Default.Action, ActionDrop)
	}
	if p.Result.Action != ActionDrop {
		t.Errorf("unknown.public.result: got %v, want %v", p.Result.Action, ActionDrop)
	}
}

func TestLookupPolicy_UnknownTool_Redacted_MasksEverything(t *testing.T) {
	p := lookupPolicy("UnknownTool", TierRedacted)
	if p.Default.Action != ActionMask {
		t.Errorf("unknown.redacted.default: got %v, want %v", p.Default.Action, ActionMask)
	}
	if p.Result.Action != ActionTruncMask {
		t.Errorf("unknown.redacted.result: got %v, want %v", p.Result.Action, ActionTruncMask)
	}
}

func TestLookupPolicy_PrivateTier_PassesThrough(t *testing.T) {
	p := lookupPolicy("UnknownTool", TierPrivate)
	if p.Default.Action != ActionPass {
		t.Errorf("private default should pass; got %v", p.Default.Action)
	}
}

func TestLookupPolicy_AllRegisteredToolsHaveBothTiers(t *testing.T) {
	for tool, perTier := range toolPolicies {
		if _, ok := perTier[TierPublic]; !ok {
			t.Errorf("%s missing TierPublic policy", tool)
		}
		if _, ok := perTier[TierRedacted]; !ok {
			t.Errorf("%s missing TierRedacted policy", tool)
		}
	}
}
