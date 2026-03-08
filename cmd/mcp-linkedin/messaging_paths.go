package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/crb2nu/loom/pkg/mcperror"
)

func legacyConversationsPath(start, count int) string {
	return fmt.Sprintf("/messaging/conversations?keyVersion=LEGACY_INBOX&start=%d&count=%d", start, count)
}

func legacyConversationMessagesPath(conversationURN string, start, count int) string {
	return fmt.Sprintf("/messaging/conversations/%s/events?start=%d&count=%d", url.PathEscape(conversationURN), start, count)
}

func (s *linkedInServer) messagingConversationsPath(ctx context.Context) (string, error) {
	queryID := strings.TrimSpace(s.conversationsQID)
	if queryID == "" {
		return "", nil
	}
	mailboxURN, err := s.resolveMailboxURN(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(mailboxURN) == "" {
		return "", errors.New("mailbox urn is empty")
	}
	return fmt.Sprintf(
		"/voyagerMessagingGraphQL/graphql?queryId=%s&variables=%s",
		url.QueryEscape(queryID),
		fmt.Sprintf("(mailboxUrn:%s)", url.QueryEscape(mailboxURN)),
	), nil
}

func (s *linkedInServer) messagingMessagesPath(conversationURN string) (string, error) {
	queryID := strings.TrimSpace(s.messagesQID)
	if queryID == "" {
		return "", nil
	}
	conversationURN = strings.TrimSpace(conversationURN)
	if conversationURN == "" {
		return "", errors.New("conversation urn is empty")
	}
	return fmt.Sprintf(
		"/voyagerMessagingGraphQL/graphql?queryId=%s&variables=%s",
		url.QueryEscape(queryID),
		fmt.Sprintf("(conversationUrn:%s)", url.QueryEscape(conversationURN)),
	), nil
}

func (s *linkedInServer) resolveMailboxURN(ctx context.Context) (string, error) {
	if configured := strings.TrimSpace(s.mailboxURN); configured != "" {
		return configured, nil
	}

	var (
		profileRaw any
		bkErr      error
	)
	if s.browserKit.mode != linkedInBrowserKitModeOff && s.mode != linkedinModeOfficial {
		if raw, err := s.requestViaBrowserKit(ctx, http.MethodGet, "/me", nil); err == nil {
			if len(raw) == 0 {
				profileRaw = map[string]any{}
			} else {
				if parseErr := json.Unmarshal(raw, &profileRaw); parseErr != nil {
					bkErr = mcperror.ParseError("LinkedIn /me browserkit response JSON", parseErr)
				}
			}
		} else {
			bkErr = err
		}
		if bkErr != nil {
			s.logger.Warn("linkedin: browserkit /me lookup failed; falling back to HTTP", "error", bkErr)
		}
	}
	if profileRaw == nil {
		var err error
		profileRaw, err = s.requestJSON(ctx, http.MethodGet, "/me", nil)
		if err != nil {
			if bkErr != nil {
				return "", mcperror.OperationFailed("resolve linkedin mailbox urn", fmt.Errorf("browserkit /me failed (%v); http /me failed (%w)", bkErr, err))
			}
			return "", err
		}
	}

	mailboxURN, err := mailboxURNFromProfile(profileRaw)
	if err != nil {
		return "", err
	}
	s.mailboxURN = mailboxURN
	return mailboxURN, nil
}

func mailboxURNFromProfile(profileRaw any) (string, error) {
	profile, ok := profileRaw.(map[string]any)
	if !ok {
		return "", errors.New("unexpected /me response shape")
	}
	miniProfile, ok := profile["miniProfile"].(map[string]any)
	if !ok {
		return "", errors.New("missing miniProfile in /me response")
	}
	dashEntityURN, _ := miniProfile["dashEntityUrn"].(string)
	dashEntityURN = strings.TrimSpace(dashEntityURN)
	if dashEntityURN == "" {
		return "", errors.New("missing miniProfile.dashEntityUrn in /me response")
	}
	return dashEntityURN, nil
}
