package termination

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AWSDetector watches the EC2 Instance Metadata Service (IMDS) for spot termination notices.
// AWS provides a 2-minute warning via the spot/instance-action endpoint.
type AWSDetector struct{}

func (d *AWSDetector) Name() string { return "aws" }

func (d *AWSDetector) Watch(ctx context.Context) (time.Duration, error) {
	// AWS requires an IMDSv2 token
	token, err := d.getToken(ctx)
	if err != nil {
		return 0, fmt.Errorf("AWS IMDS token: %w", err)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
			body, err := fetchURL(ctx,
				"http://169.254.169.254/latest/meta-data/spot/instance-action",
				map[string]string{"X-aws-ec2-metadata-token": token},
			)
			if err != nil {
				continue // Not terminated yet
			}

			// Response contains "action" and "time" fields
			// Example: {"action": "terminate", "time": "2025-01-01T00:02:00Z"}
			if strings.Contains(body, "terminate") || strings.Contains(body, "stop") {
				// AWS gives exactly 2 minutes warning
				return 2 * time.Minute, nil
			}
		}
	}
}

func (d *AWSDetector) getToken(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		"http://169.254.169.254/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "300")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("IMDS token request returned %d", resp.StatusCode)
	}

	token := make([]byte, 1024)
	n, _ := resp.Body.Read(token)
	return string(token[:n]), nil
}
