// Command voice-loop is a demonstrator for the flexinfer voice stack's full
// conversational loop (ASR → LLM → TTS) through a single proxy base URL.
//
// Usage:
//
//	voice-loop -proxy http://localhost:18080 -text "what time is the meeting" -out reply.wav -verify
//	voice-loop -proxy http://localhost:18080 -audio question.wav -out reply.wav -verify
//
// With -verify the synthesized reply is transcribed back through Whisper so the
// round-trip transcript can be eyeballed against the model's reply.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/flexinfer/flexinfer/internal/voiceloop"
)

func main() {
	proxy := flag.String("proxy", "http://localhost:18080", "flexinfer proxy base URL")
	audioPath := flag.String("audio", "", "path to an input audio file (speech). Mutually exclusive with -text")
	text := flag.String("text", "", "input text (skips ASR). Mutually exclusive with -audio")
	chatModel := flag.String("chat-model", "gemma4-26b-a4b-gptq", "chat model for /v1/chat/completions")
	asrModel := flag.String("asr-model", "whisper-large-v3-turbo", "ASR model for /v1/audio/transcriptions")
	ttsModel := flag.String("tts-model", "kokoro", "TTS model for /v1/audio/speech")
	voice := flag.String("voice", "af_heart", "Kokoro voice id")
	format := flag.String("format", "wav", "TTS audio format (wav, mp3)")
	system := flag.String("system", "You are a concise voice assistant. Answer in one short sentence.", "system prompt")
	maxTokens := flag.Int("max-tokens", 256, "max chat reply tokens")
	out := flag.String("out", "reply.wav", "output path for synthesized reply audio")
	verify := flag.Bool("verify", false, "transcribe the TTS output back through ASR (intelligibility check)")
	flag.Parse()

	if err := run(*proxy, *audioPath, *text, *out, voiceloop.Config{
		ChatModel: *chatModel,
		ASRModel:  *asrModel,
		TTSModel:  *ttsModel,
		Voice:     *voice,
		Format:    *format,
		System:    *system,
		MaxTokens: *maxTokens,
		Verify:    *verify,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(proxy, audioPath, text, out string, cfg voiceloop.Config) error {
	if (audioPath == "") == (text == "") {
		return fmt.Errorf("provide exactly one of -audio or -text")
	}

	var audio []byte
	var audioName string
	if audioPath != "" {
		b, err := os.ReadFile(audioPath)
		if err != nil {
			return fmt.Errorf("read audio: %w", err)
		}
		audio = b
		audioName = audioPath
	}

	c := voiceloop.NewClient(proxy, nil)
	res, err := voiceloop.Run(context.Background(), c, cfg, audio, audioName, text)
	if err != nil {
		return err
	}

	if res.Transcript != "" {
		fmt.Printf("ASR transcript : %s\n", res.Transcript)
	}
	fmt.Printf("LLM reply      : %s\n", res.Reply)
	if err := os.WriteFile(out, res.Audio, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Printf("TTS audio      : %d bytes (%s) → %s\n", len(res.Audio), res.ContentType, out)
	if cfg.Verify {
		fmt.Printf("Round-trip ASR : %s\n", res.Verification)
	}
	return nil
}
