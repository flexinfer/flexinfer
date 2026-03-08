package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mcperror"
)

func (s *linkedInServer) requestJSON(ctx context.Context, method, path string, body any) (any, error) {
	raw, err := s.request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, mcperror.ParseError("LinkedIn API response JSON", err)
	}
	return out, nil
}

func (s *linkedInServer) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	if s.shouldUseBrowserKitTransport(path) {
		payload, err := s.requestViaBrowserKit(ctx, method, path, body)
		if err == nil {
			return payload, nil
		}
		s.logger.Warn("linkedin browserkit primary transport failed; falling back to http", "path", path, "error", err)
	}
	return s.requestWithRecovery(ctx, method, path, body, true)
}

func (s *linkedInServer) requestWithRecovery(ctx context.Context, method, path string, body any, allowRecovery bool) ([]byte, error) {
	payload, err := s.doRequest(ctx, method, path, body)
	if err == nil {
		return payload, nil
	}

	if (!allowRecovery || !isAuthChallengeErr(err)) && isAuthChallengeErr(err) && s.shouldUseBrowserKitTransport(path) {
		s.logger.Warn("linkedin auth challenge detected; attempting browserkit transport fallback", "path", path)
		return s.requestViaBrowserKit(ctx, method, path, body)
	}

	if !allowRecovery || !isAuthChallengeErr(err) {
		return nil, err
	}

	s.logger.Warn("linkedin auth challenge detected; attempting one-time recovery", "path", path)
	if recErr := s.maybeRecoverAfterChallenge(ctx, path); recErr != nil {
		return nil, recErr
	}
	return s.requestWithRecovery(ctx, method, path, body, false)
}

func (s *linkedInServer) doRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reqBody *bytes.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, mcperror.ParseError("request body", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	} else {
		reqBody = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reqBody)
	if err != nil {
		return nil, mcperror.OperationFailed("create request", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-RestLi-Protocol-Version", "2.0.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.accessToken)
	}
	if s.sessionToken != "" {
		cookieValue := "li_at=" + s.sessionToken
		if normalized := normalizeJSessionID(s.jsessionID); normalized != "" {
			cookieValue += "; JSESSIONID=" + normalized
		}
		req.Header.Set("Cookie", cookieValue)
	}
	if csrfToken := csrfTokenFromJSessionID(s.jsessionID); csrfToken != "" {
		req.Header.Set("csrf-token", csrfToken)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "too many redirects") || strings.Contains(msg, "stopped after 10 redirects") {
			return nil, &authChallengeError{statusCode: 0, body: err.Error()}
		}
		return nil, mcperror.WrapAPI("LinkedIn", err)
	}
	defer resp.Body.Close()

	payload, truncated, err := httpclient.ReadBodyWithLimit(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, mcperror.OperationFailed("read LinkedIn API response", err)
	}
	if truncated {
		return nil, mcperror.ServerError("LinkedIn response exceeded 2MB limit")
	}
	if isLinkedInSessionInvalidation(resp, payload) {
		return nil, &authChallengeError{statusCode: resp.StatusCode, body: "LinkedIn invalidated session cookies"}
	}
	if resp.StatusCode >= 400 {
		if isLinkedInAuthChallenge(resp.StatusCode, payload) {
			return nil, &authChallengeError{statusCode: resp.StatusCode, body: string(payload)}
		}
		return nil, mcperror.APIError("LinkedIn", resp.StatusCode, string(payload))
	}

	return payload, nil
}

func normalizeJSessionID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	trimmed := strings.Trim(v, "\"")
	return `"` + trimmed + `"`
}

func csrfTokenFromJSessionID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return strings.Trim(v, "\"")
}
