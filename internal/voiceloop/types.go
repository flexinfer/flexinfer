package voiceloop

// Config holds the per-run knobs for the conversational loop.
type Config struct {
	// ChatModel is the OpenAI `model` for /v1/chat/completions (e.g. the
	// gemma4-26b textgen Model CR). Resolved by the proxy's model router.
	ChatModel string
	// ASRModel is the `model` for /v1/audio/transcriptions (Whisper Model CR).
	ASRModel string
	// TTSModel is the `model` for /v1/audio/speech (Kokoro: "kokoro").
	TTSModel string
	// Voice is the Kokoro voice id (e.g. "af_heart").
	Voice string
	// Format is the TTS response audio format ("wav", "mp3", ...).
	Format string
	// System is an optional system prompt prepended to the chat turn.
	System string
	// MaxTokens caps the chat reply length.
	MaxTokens int
	// Verify, when true, transcribes the TTS output back through ASR so the
	// round-trip transcript can be compared to Reply (intelligibility check).
	Verify bool
}

// Result captures every stage of one loop pass.
type Result struct {
	// Transcript is the ASR output of the input audio (empty for text input).
	Transcript string
	// Prompt is the text actually sent to the LLM (Transcript or the raw text).
	Prompt string
	// Reply is the LLM's assistant message.
	Reply string
	// Audio is the synthesized speech bytes for Reply.
	Audio []byte
	// ContentType is the TTS response Content-Type (e.g. "audio/wav").
	ContentType string
	// Verification is the round-trip ASR transcript of Audio (Verify only).
	Verification string
}
