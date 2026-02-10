package termination

import (
	"context"
	"strings"
	"time"
)

// GCPDetector watches the GCE metadata server for preemptible/spot VM termination.
// GCP provides a 30-second warning via the maintenance-event endpoint.
type GCPDetector struct{}

func (d *GCPDetector) Name() string { return "gcp" }

func (d *GCPDetector) Watch(ctx context.Context) (time.Duration, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	headers := map[string]string{"Metadata-Flavor": "Google"}

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
			// Check for preemption via maintenance event
			body, err := fetchURL(ctx,
				"http://169.254.169.254/computeMetadata/v1/instance/maintenance-event",
				headers,
			)
			if err != nil {
				continue
			}

			body = strings.TrimSpace(body)
			if body == "TERMINATE_ON_HOST_MAINTENANCE" || body == "PREEMPT" {
				// GCP gives 30 seconds for preemptible VMs
				return 30 * time.Second, nil
			}

			// Also check the preempted metadata key
			preempted, err := fetchURL(ctx,
				"http://169.254.169.254/computeMetadata/v1/instance/preempted",
				headers,
			)
			if err == nil && strings.TrimSpace(preempted) == "TRUE" {
				return 30 * time.Second, nil
			}
		}
	}
}
