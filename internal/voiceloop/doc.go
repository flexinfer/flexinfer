// Package voiceloop is a demonstrator for the flexinfer voice stack's full
// conversational loop: speech-in → transcription (ASR) → chat (LLM) →
// speech-out (TTS), all through the single flexinfer-proxy base URL.
//
// It exercises three OpenAI-compatible routes the proxy now exposes:
//
//	POST /v1/audio/transcriptions  → Whisper Model CR (gfx1100)   [ASR]
//	POST /v1/chat/completions      → gemma4-26b Model CR (gfx1100) [LLM]
//	POST /v1/audio/speech          → Kokoro TTS Deployment (CPU)   [TTS]
//
// With Verify enabled, the TTS audio is fed back through ASR so the round-trip
// transcript can be compared to the model's reply — a machine check that the
// synthesized speech is intelligible, not just non-silent bytes.
package voiceloop
