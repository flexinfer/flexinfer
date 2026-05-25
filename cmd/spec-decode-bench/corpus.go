package main

// CorpusEntry is one (name, prompt) pair used by the bench. The name is a
// short stable identifier suitable for use as a JSON object key; the prompt
// is the actual text fed to Draft/Verify (or their mocks).
type CorpusEntry struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// DefaultCorpus returns a small, hard-coded set of prompts that exercise a
// mix of conversational, code, summarization, and non-English content. It
// is intentionally small so the bench runs in seconds against the mock
// timing model.
func DefaultCorpus() []CorpusEntry {
	return []CorpusEntry{
		{
			Name:   "short_question",
			Prompt: "What is the capital of France?",
		},
		{
			Name:   "casual_chat",
			Prompt: "Hey, how are you doing today?",
		},
		{
			Name:   "factual_explain",
			Prompt: "Explain in one paragraph what TCP congestion control does.",
		},
		{
			Name:   "code_snippet_python",
			Prompt: "Write a Python function that returns the nth Fibonacci number.",
		},
		{
			Name:   "code_snippet_go",
			Prompt: "Show me a Go function that reverses a string in place.",
		},
		{
			Name: "summarize_paragraph",
			Prompt: "Summarize the following in one sentence: Kubernetes is an " +
				"open-source container orchestration system originally designed " +
				"by Google and now maintained by the CNCF. It groups containers " +
				"into pods and schedules them across a cluster of worker nodes.",
		},
		{
			Name:   "instruction_followup",
			Prompt: "Given the list [3, 1, 4, 1, 5, 9, 2, 6], sort it ascending and explain the steps.",
		},
		{
			Name:   "reasoning_short",
			Prompt: "If a train leaves Chicago at 3pm going 60 mph and another leaves Denver at 4pm going 80 mph, when do they meet?",
		},
		{
			Name:   "cjk_intro",
			Prompt: "请用一句话介绍 Kubernetes。",
		},
		{
			Name:   "creative_open",
			Prompt: "Write the opening sentence of a noir detective novel set on Mars.",
		},
	}
}
