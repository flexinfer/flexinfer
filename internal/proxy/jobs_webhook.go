package proxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Job webhooks follow Standard Webhooks (https://www.standardwebhooks.com):
// every delivery is a POST with webhook-id, webhook-timestamp and, when the
// job names a secret, webhook-signature = "v1," + base64(HMAC-SHA256(secret,
// id + "." + timestamp + "." + body)). The id is "<job>.<event>", the same on
// every retry of one event, so receivers dedupe on it.

// Job webhook events.
const (
	jobEventInProgress = "job.in_progress"
	jobEventCompleted  = "job.completed"
	jobEventFailed     = "job.failed"
	jobEventCancelled  = "job.cancelled"
	jobEventIncomplete = "job.incomplete"
)

// defaultJobWebhookEvents are delivered when spec.webhook.events is empty.
var defaultJobWebhookEvents = []string{jobEventCompleted, jobEventFailed, jobEventCancelled, jobEventIncomplete}

// jobWebhookRetrySchedule is the wait before each delivery attempt of one
// event; the first is immediate.
var jobWebhookRetrySchedule = []time.Duration{0, 5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

const jobWebhookTimeout = 10 * time.Second

// jobWebhookEvent is the delivered body.
type jobWebhookEvent struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      jobObject `json:"data"`
}

// webhookSecretBytes decodes a "whsec_"-prefixed base64 secret; any other
// value is used as raw bytes.
func webhookSecretBytes(secret string) []byte {
	if rest, ok := strings.CutPrefix(secret, "whsec_"); ok {
		if b, err := base64.StdEncoding.DecodeString(rest); err == nil {
			return b
		}
	}
	return []byte(secret)
}

// signWebhook returns the webhook-signature header value.
func signWebhook(secret []byte, id string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(id + "." + strconv.FormatInt(timestamp, 10) + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// jobWebhookWants reports whether events (empty = the defaults) includes event.
func jobWebhookWants(events []string, event string) bool {
	if len(events) == 0 {
		events = defaultJobWebhookEvents
	}
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}

// deliverWebhook makes one delivery attempt.
func deliverWebhook(ctx context.Context, client *http.Client, url string, secret []byte, id string, now time.Time, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, jobWebhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	ts := now.Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts, 10))
	if len(secret) > 0 {
		req.Header.Set("webhook-signature", signWebhook(secret, id, ts, body))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook %s answered %d", url, resp.StatusCode)
	}
	return nil
}

// marshalJobWebhookEvent renders the delivery body for event.
func marshalJobWebhookEvent(event string, at time.Time, job jobObject) ([]byte, error) {
	return json.Marshal(jobWebhookEvent{Type: event, Timestamp: at.UTC(), Data: job})
}
