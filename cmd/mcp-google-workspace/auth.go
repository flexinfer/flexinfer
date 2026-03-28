package main

import (
	"context"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"google.golang.org/api/option"

	calendar "google.golang.org/api/calendar/v3"
	docsapi "google.golang.org/api/docs/v1"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/people/v1"

	"github.com/crb2nu/loom/pkg/googleworkspace"
	"github.com/crb2nu/loom/pkg/mcperror"
)

func (s *googleWorkspaceServer) handleAuthStatus(ctx context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	creds, err := googleworkspace.LoadRuntimeCredentials(s.secrets)
	if err != nil {
		return mcp.JSONResult(map[string]any{
			"configured": false,
			"error":      err.Error(),
			"hint":       "Run `loom auth google login` to populate the Loom secret store.",
		})
	}

	result := map[string]any{
		"configured":        true,
		"account_email":     creds.AccountEmail,
		"scopes":            creds.Scopes,
		"refresh_token_set": creds.RefreshToken != "",
		"client_id_present": creds.ClientID != "",
		"client_secret_set": creds.ClientSecret != "",
	}

	token, tokenErr := creds.AccessToken(ctx, s.httpClient.HTTP())
	if tokenErr == nil && token != nil {
		result["access_token_expiry"] = token.Expiry.Format(time.RFC3339)
		result["token_refresh_ok"] = true
	} else if tokenErr != nil {
		result["token_refresh_ok"] = false
		result["token_refresh_error"] = tokenErr.Error()
	}

	if info, infoErr := googleworkspace.FetchUserInfo(ctx, s.httpClient.HTTP(), creds); infoErr == nil {
		result["userinfo"] = info
		if creds.AccountEmail == "" {
			result["account_email"] = info.Email
		}
	} else {
		result["userinfo_error"] = infoErr.Error()
	}

	return mcp.JSONResult(result)
}

func (s *googleWorkspaceServer) newClients(ctx context.Context) (*googleClients, *googleworkspace.Credentials, error) {
	creds, err := googleworkspace.LoadRuntimeCredentials(s.secrets)
	if err != nil {
		return nil, nil, mcperror.NotConfigured(
			googleworkspace.SecretRefreshToken,
			"run `loom auth google login` (and ensure client credentials are stored in Loom secrets)",
		)
	}
	authHTTPClient, err := creds.NewHTTPClient(ctx, s.httpClient.HTTP())
	if err != nil {
		return nil, nil, s.wrapGoogleError("Google OAuth", err)
	}
	gmailSvc, err := gmail.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		return nil, nil, s.wrapGoogleError("Gmail", err)
	}
	calendarSvc, err := calendar.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		return nil, nil, s.wrapGoogleError("Calendar", err)
	}
	docsSvc, err := docsapi.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		return nil, nil, s.wrapGoogleError("Docs", err)
	}
	driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		return nil, nil, s.wrapGoogleError("Drive", err)
	}
	peopleSvc, err := people.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		// People API is not required for the current toolset.
		peopleSvc = nil
	}
	return &googleClients{
		http:     authHTTPClient,
		gmail:    gmailSvc,
		calendar: calendarSvc,
		docs:     docsSvc,
		drive:    driveSvc,
		people:   peopleSvc,
	}, creds, nil
}
