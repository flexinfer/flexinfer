package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/pkg/mcperror"
)

// tfcRequest makes an authenticated request to Terraform Cloud API
func tfcRequest(ctx context.Context, method, path string) (map[string]any, error) {
	apiURL := strings.TrimSuffix(tfcHost, "/") + "/api/v2" + path

	req, err := http.NewRequestWithContext(ctx, method, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+tfcToken)
	req.Header.Set("Content-Type", "application/vnd.api+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]any
		if json.Unmarshal(body, &errResp) == nil {
			if errors, ok := errResp["errors"].([]any); ok && len(errors) > 0 {
				if errObj, ok := errors[0].(map[string]any); ok {
					return nil, mcperror.APIError("Terraform Cloud", resp.StatusCode, fmt.Sprintf("%v", errObj["detail"]))
				}
			}
		}
		return nil, mcperror.APIError("Terraform Cloud", resp.StatusCode, string(body))
	}

	var result map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
	}

	return result, nil
}
