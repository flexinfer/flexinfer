package voiceloop

import (
	"context"
	"fmt"
	"strings"
)

// Run executes one conversational-loop pass through the proxy:
//
//	(audio) ──ASR──> transcript ─┐
//	                              ├─> LLM ──> reply ──TTS──> audio ─(Verify)─> ASR
//	(text) ──────────────────────┘
//
// Exactly one of audio or text must be non-empty. With cfg.Verify the
// synthesized audio is transcribed back so the caller can compare it to Reply.
func Run(ctx context.Context, c *Client, cfg Config, audio []byte, audioName, text string) (*Result, error) {
	res := &Result{}

	prompt := strings.TrimSpace(text)
	if len(audio) > 0 {
		t, err := c.Transcribe(ctx, audio, audioName, cfg.ASRModel)
		if err != nil {
			return nil, fmt.Errorf("asr: %w", err)
		}
		res.Transcript = t
		prompt = t
	}
	if prompt == "" {
		return nil, fmt.Errorf("no input: provide audio or non-empty text")
	}
	res.Prompt = prompt

	reply, err := c.Chat(ctx, cfg.System, prompt, cfg.ChatModel, cfg.MaxTokens)
	if err != nil {
		return nil, fmt.Errorf("chat: %w", err)
	}
	res.Reply = reply

	speech, ct, err := c.Speak(ctx, reply, cfg.Voice, cfg.TTSModel, cfg.Format)
	if err != nil {
		return nil, fmt.Errorf("tts: %w", err)
	}
	res.Audio = speech
	res.ContentType = ct

	if cfg.Verify {
		v, err := c.Transcribe(ctx, speech, "reply."+cfg.Format, cfg.ASRModel)
		if err != nil {
			return res, fmt.Errorf("verify asr: %w", err)
		}
		res.Verification = v
	}
	return res, nil
}
