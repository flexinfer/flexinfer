// Package termination provides cloud-provider-specific spot instance termination detection.
// Each detector polls the respective metadata endpoint to detect upcoming termination,
// allowing FlexInfer to gracefully drain GPU workloads before the instance is reclaimed.
package termination

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TerminationDetector watches for cloud provider spot/preemptible termination signals.
type TerminationDetector interface {
	// Watch blocks until a termination signal is detected or the context is cancelled.
	// Returns the estimated time remaining before termination.
	Watch(ctx context.Context) (timeRemaining time.Duration, err error)

	// Name returns the detector's identifier (e.g., "aws", "gcp", "azure", "harvester", "generic").
	Name() string
}

// AutoDetect probes metadata endpoints to determine the cloud provider and returns
// the appropriate TerminationDetector. Falls back to GenericDetector if no provider is detected.
func AutoDetect(ctx context.Context) TerminationDetector {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 2 * time.Second}

	// Try AWS IMDS
	if isReachable(probeCtx, client, "http://169.254.169.254/latest/meta-data/", "PUT") {
		return &AWSDetector{}
	}

	// Try GCP metadata
	req, _ := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://169.254.169.254/computeMetadata/v1/", nil)
	if req != nil {
		req.Header.Set("Metadata-Flavor", "Google")
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return &GCPDetector{}
			}
		}
	}

	// Try Azure IMDS
	if isReachable(probeCtx, client, "http://169.254.169.254/metadata/instance?api-version=2021-02-01", "GET") {
		return &AzureDetector{}
	}

	return &GenericDetector{}
}

func isReachable(ctx context.Context, client *http.Client, url, method string) bool {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500
}

func fetchURL(ctx context.Context, url string, headers map[string]string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
