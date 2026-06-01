package agentloop

// Conversation is the append-only, mutability-ordered context that makes the
// prefix cache pay off. The system message (system prompt + the tool set,
// which is carried separately in the request) is immutable; history grows by
// Append only and is never reordered or rewritten. That invariant is the
// whole point of F4-tool-loop-as-prefix: every turn's message slice is a
// block-aligned prefix extension of the previous turn's, so vLLM reuses the
// cached KV for everything before the new tail.
type Conversation struct {
	system  string
	history []Message
}

// NewConversation starts a conversation with an immutable system prompt.
func NewConversation(system string) *Conversation {
	return &Conversation{system: system}
}

// Append adds a message to the end of history. This is the ONLY mutation a
// caller can make — there is deliberately no Insert, Replace, or Reorder,
// because any of those would invalidate the prefix.
func (c *Conversation) Append(m Message) {
	c.history = append(c.history, m)
}

// Messages returns the full wire-order slice: the system message first, then
// history in append order. The returned slice is a fresh copy so callers
// cannot mutate internal state.
func (c *Conversation) Messages() []Message {
	out := make([]Message, 0, len(c.history)+1)
	if c.system != "" {
		out = append(out, Message{Role: RoleSystem, Content: c.system})
	}
	out = append(out, c.history...)
	return out
}

// History returns a copy of the appended (non-system) messages.
func (c *Conversation) History() []Message {
	out := make([]Message, len(c.history))
	copy(out, c.history)
	return out
}

// Rounds counts appended messages, a cheap proxy for conversation growth.
func (c *Conversation) Rounds() int { return len(c.history) }
