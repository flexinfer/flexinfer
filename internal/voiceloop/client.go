package voiceloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Client speaks the three OpenAI-compatible voice routes against the flexinfer
// proxy base URL.
type Client struct {
	httpClient *http.Client
	endpoint   string
}

// NewClient returns a Client for the given proxy base URL. A nil http client
// gets a default with a generous timeout (cold starts can preempt the 26B).
func NewClient(endpoint string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Client{httpClient: hc, endpoint: strings.TrimRight(endpoint, "/")}
}

// Transcribe posts audio to /v1/audio/transcriptions (multipart form) and
// returns the recognized text.
func (c *Client) Transcribe(ctx context.Context, audio []byte, filename, model string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(audio); err != nil {
		return "", fmt.Errorf("write audio: %w", err)
	}
	if err := mw.WriteField("model", model); err != nil {
		return "", fmt.Errorf("write model field: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", fmt.Errorf("new asr request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	body, err := c.do(req)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode asr response: %w (body=%s)", err, truncate(body, 256))
	}
	return strings.TrimSpace(parsed.Text), nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat posts a single user turn (with optional system prompt) to
// /v1/chat/completions and returns the assistant reply.
func (c *Client) Chat(ctx context.Context, system, user, model string, maxTokens int) (string, error) {
	msgs := make([]chatMessage, 0, 2)
	if system != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: user})

	reqBody := map[string]any{
		"model":      model,
		"messages":   msgs,
		"max_tokens": maxTokens,
		"stream":     false,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("new chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	body, err := c.do(req)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode chat response: %w (body=%s)", err, truncate(body, 256))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("chat: empty choices array")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

// Speak posts text to /v1/audio/speech and returns the synthesized audio bytes
// plus the response Content-Type.
func (c *Client) Speak(ctx context.Context, text, voice, model, format string) ([]byte, string, error) {
	reqBody := map[string]any{
		"model":           model,
		"voice":           voice,
		"input":           text,
		"response_format": format,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "", fmt.Errorf("marshal speech request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/audio/speech", bytes.NewReader(payload))
	if err != nil {
		return nil, "", fmt.Errorf("new speech request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("speech http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read speech body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("speech: status %d: %s", resp.StatusCode, truncate(body, 256))
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// do executes a JSON-returning request and returns the body, mapping non-2xx
// to an error.
func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(body, 256))
	}
	return body, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}
