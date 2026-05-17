package gates

// rubricGroundingInstructions is the anti-hallucination boilerplate every
// LLM-judged rubric prompt appends after its own scoring instructions but
// before the inputs / diff. It anchors the judge to the actual diff and
// defines explicit empty-input behavior so the model doesn't fabricate
// references to files, symbols, or behaviors that aren't in the change.
//
// Live trigger (post-M6 canary PIPE-MILLS-CANARY-M6-164007-1779036007,
// 2026-05-17): with `DiffPatch` populated but the change being a one-line
// markdown heartbeat bump, gemma4-26b-a4b-gptq replied
//
//	score=0.00 below threshold=0.70 | Example: file.py:10 - debug print found
//
// — fabricating a Python file that doesn't exist anywhere in the diff.
// The fix is prompt-engineering, not a model swap: gemma is the only Ready
// model in the cluster's flexinfer.
//
// The exact phrasing is asserted by the template snapshot tests in
// pkg/mills/clients/flexinfer_test.go. Changing the wording requires
// updating those tests; treating the string as a constant makes future
// drift intentional rather than accidental.
const rubricGroundingInstructions = `Ground every concern in EXACTLY ONE specific line of the diff provided ` +
	`below. Do NOT reference files, symbols, line numbers, or behaviors that ` +
	`are not present in the diff. If the diff is empty or contains only ` +
	`documentation / fixture / metadata changes, state that explicitly and ` +
	`return a score of 1.0 (no scorable concerns).`

// rubricStructuralOutputInstructions is the response-envelope contract
// every rubric template appends at the very end. parseRubricEnvelope in
// pkg/mills/clients/flexinfer.go consumes
//
//	{"score": <float in [0,1]>, "reasons": [...]}
//
// and treats free-text replies as unparseable (M2.5 routes those through
// the gate-retry path instead of escalating, but a parseable verdict is
// always cheaper than a retry).
//
// Pinned in the snapshot test in pkg/mills/clients/flexinfer_test.go so
// any future drift is intentional. The empty-diff branch matches the
// score 1.0 fallback in rubricGroundingInstructions — both nudge the
// model toward the same outcome.
const rubricStructuralOutputInstructions = "Respond ONLY with a JSON object matching:\n" +
	"```\n" +
	`{"score": <number between 0.0 and 1.0>, "reasons": ["<one concern per array entry>"]}` + "\n" +
	"```\n" +
	"Do not include any text outside the JSON object. Do not ask clarifying " +
	"questions. If the diff is empty or fixture-only, respond " +
	`{"score": 1.0, "reasons": ["fixture-only or empty diff; no scorable concerns"]}.`

// RubricGroundingInstructions is the exported view of the
// anti-hallucination boilerplate so external snapshot tests and
// future rubric authors can reference the canonical string without
// duplicating it. Keep the unexported `rubricGroundingInstructions`
// for in-template concatenation; this alias exists solely for test
// + documentation reuse.
const RubricGroundingInstructions = rubricGroundingInstructions

// RubricStructuralOutputInstructions is the exported view of the
// JSON-envelope instructions. Same reasoning as
// RubricGroundingInstructions.
const RubricStructuralOutputInstructions = rubricStructuralOutputInstructions
