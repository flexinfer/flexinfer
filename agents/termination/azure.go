package termination

import (
	"context"
	"strings"
	"time"
)

// AzureDetector watches the Azure Instance Metadata Service for spot eviction.
// Azure provides a 30-second warning via the scheduled events endpoint.
type AzureDetector struct{}

func (d *AzureDetector) Name() string { return "azure" }

func (d *AzureDetector) Watch(ctx context.Context) (time.Duration, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	headers := map[string]string{"Metadata": "true"}

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
			// Check scheduled events for Preempt events
			body, err := fetchURL(ctx,
				azureScheduledEventsURL(),
				headers,
			)
			if err != nil {
				continue
			}

			// Azure returns JSON with "Events" array containing "EventType": "Preempt"
			if strings.Contains(body, "Preempt") {
				return 30 * time.Second, nil
			}
		}
	}
}
