package main

import (
	"strings"

	"github.com/crb2nu/loom/pkg/strutil"
)

func formatConversationsResult(raw any, start, count int, includeRaw bool, source string) map[string]any {
	elements, syncToken := extractConversationElements(raw)
	total := len(elements)
	sliced := paginateAnySlice(elements, start, count)
	conversations := make([]map[string]any, 0, len(sliced))
	for _, item := range sliced {
		if m, ok := item.(map[string]any); ok {
			conversations = append(conversations, summarizeConversation(m))
		}
	}

	out := map[string]any{
		"source":        source,
		"conversations": conversations,
		"pagination": map[string]any{
			"requested_start":        start,
			"requested_count":        count,
			"returned_count":         len(conversations),
			"available_count":        total,
			"client_side_pagination": source == "graphql",
		},
	}
	if syncToken != "" {
		out["sync_token"] = syncToken
	}
	if includeRaw {
		out["raw"] = raw
	}
	return out
}

func formatConversationMessagesResult(raw any, conversationURN string, start, count int, includeRaw bool, source string) map[string]any {
	elements, syncToken := extractMessageElements(raw)
	total := len(elements)
	sliced := paginateAnySlice(elements, start, count)
	messages := make([]map[string]any, 0, len(sliced))
	for _, item := range sliced {
		if m, ok := item.(map[string]any); ok {
			messages = append(messages, summarizeMessage(m, conversationURN))
		}
	}

	out := map[string]any{
		"source":           source,
		"conversation_urn": conversationURN,
		"messages":         messages,
		"pagination": map[string]any{
			"requested_start":        start,
			"requested_count":        count,
			"returned_count":         len(messages),
			"available_count":        total,
			"client_side_pagination": source == "graphql",
		},
	}
	if syncToken != "" {
		out["sync_token"] = syncToken
	}
	if includeRaw {
		out["raw"] = raw
	}
	return out
}

func formatProfileResult(raw any, includeRaw bool) map[string]any {
	profile, _ := raw.(map[string]any)
	mini, _ := profile["miniProfile"].(map[string]any)

	firstName := attributedText(mini["firstName"])
	lastName := attributedText(mini["lastName"])
	out := map[string]any{
		"profile": map[string]any{
			"entity_urn":        stringValue(profile["entityUrn"]),
			"first_name":        firstName,
			"last_name":         lastName,
			"full_name":         strings.TrimSpace(firstName + " " + lastName),
			"headline":          attributedText(mini["headline"]),
			"occupation":        attributedText(profile["occupation"]),
			"public_identifier": stringValue(mini["publicIdentifier"]),
			"profile_url":       firstNonEmpty(stringValue(mini["publicProfileUrl"]), stringValue(mini["profileUrl"])),
			"dash_entity_urn":   stringValue(mini["dashEntityUrn"]),
			"memorialized":      boolValue(profile["memorialized"]),
		},
	}
	if includeRaw {
		out["raw"] = raw
	}
	return out
}

func formatSendMessageResult(raw any, conversationURN string, recipients []string, text, subject string, includeRaw bool) map[string]any {
	out := map[string]any{
		"ok": true,
		"request": map[string]any{
			"conversation_urn": conversationURN,
			"recipients":       recipients,
			"recipients_count": len(recipients),
			"subject":          subject,
			"text_preview":     strutil.TruncateSingleLine(text, 240),
		},
		"result": summarizeSendMutationResult(raw, conversationURN),
	}
	if includeRaw {
		out["raw"] = raw
	}
	return out
}

func summarizeSendMutationResult(raw any, fallbackConversationURN string) map[string]any {
	out := map[string]any{
		"conversation_urn": strings.TrimSpace(fallbackConversationURN),
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return out
	}

	if conversationURN := firstNonEmpty(
		nestedString(root, "conversation", "entityUrn"),
		nestedString(root, "value", "conversation", "entityUrn"),
		nestedString(root, "event", "conversation", "entityUrn"),
	); conversationURN != "" {
		out["conversation_urn"] = conversationURN
	}
	if eventURN := firstNonEmpty(
		nestedString(root, "event", "entityUrn"),
		nestedString(root, "event", "backendUrn"),
		stringValue(root["entityUrn"]),
		stringValue(root["backendUrn"]),
	); eventURN != "" {
		out["event_urn"] = eventURN
	}
	if createdAt := firstNonZero(
		nestedInt64(root, "event", "createdAt"),
		int64Value(root["createdAt"]),
	); createdAt > 0 {
		out["created_at"] = createdAt
	}

	return out
}

func extractConversationElements(raw any) ([]any, string) {
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, ""
	}

	if data, ok := root["data"].(map[string]any); ok {
		if coll, ok := data["messengerConversationsBySyncToken"].(map[string]any); ok {
			elements := toAnySlice(coll["elements"])
			syncToken := extractSyncToken(coll)
			if syncToken == "" {
				syncToken = extractSyncToken(data)
			}
			return elements, syncToken
		}
	}

	return toAnySlice(root["elements"]), extractSyncToken(root)
}

func extractMessageElements(raw any) ([]any, string) {
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, ""
	}

	if data, ok := root["data"].(map[string]any); ok {
		if coll, ok := data["messengerMessagesBySyncToken"].(map[string]any); ok {
			elements := toAnySlice(coll["elements"])
			syncToken := extractSyncToken(coll)
			if syncToken == "" {
				syncToken = extractSyncToken(data)
			}
			return elements, syncToken
		}
	}

	return toAnySlice(root["elements"]), extractSyncToken(root)
}

func summarizeConversation(in map[string]any) map[string]any {
	conversationURN := stringValue(in["entityUrn"])
	backendURN := stringValue(in["backendUrn"])
	if conversationURN == "" {
		conversationURN = backendURN
	}

	out := map[string]any{
		"conversation_urn": conversationURN,
		"entity_urn":       stringValue(in["entityUrn"]),
		"backend_urn":      backendURN,
		"conversation_url": stringValue(in["conversationUrl"]),
		"title":            stringValue(in["title"]),
		"state":            stringValue(in["state"]),
		"read":             boolValue(in["read"]),
		"unread_count":     intValue(in["unreadCount"]),
		"last_activity_at": int64Value(in["lastActivityAt"]),
		"created_at":       int64Value(in["createdAt"]),
		"categories":       stringSliceValue(in["categories"]),
		"participants":     summarizeParticipants(in["conversationParticipants"]),
	}
	if latest := summarizeLatestMessage(in["messages"]); latest != nil {
		out["latest_message"] = latest
	}
	return out
}

func summarizeParticipants(raw any) []map[string]any {
	items := toAnySlice(raw)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		memberInfo := map[string]any{}
		if participantType, ok := m["participantType"].(map[string]any); ok {
			if member, ok := participantType["member"].(map[string]any); ok {
				memberInfo = member
			}
		}
		name := strings.TrimSpace(strings.TrimSpace(attributedText(memberInfo["firstName"])) + " " + strings.TrimSpace(attributedText(memberInfo["lastName"])))
		out = append(out, map[string]any{
			"entity_urn":        stringValue(m["entityUrn"]),
			"host_identity_urn": stringValue(m["hostIdentityUrn"]),
			"name":              name,
			"profile_url":       stringValue(memberInfo["profileUrl"]),
			"headline":          attributedText(memberInfo["headline"]),
			"distance":          stringValue(memberInfo["distance"]),
			"member_badge_type": stringValue(m["memberBadgeType"]),
		})
	}
	return out
}

func summarizeLatestMessage(raw any) map[string]any {
	messages, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	elements := toAnySlice(messages["elements"])
	if len(elements) == 0 {
		return nil
	}
	msg, ok := elements[0].(map[string]any)
	if !ok {
		return nil
	}
	body := attributedText(msg["body"])
	out := map[string]any{
		"entity_urn":    stringValue(msg["entityUrn"]),
		"backend_urn":   stringValue(msg["backendUrn"]),
		"subject":       stringValue(msg["subject"]),
		"body":          body,
		"body_preview":  strutil.TruncateSingleLine(body, 240),
		"delivered_at":  int64Value(msg["deliveredAt"]),
		"sender_entity": extractSenderEntityURN(msg),
	}
	return out
}

func summarizeMessage(in map[string]any, requestedConversationURN string) map[string]any {
	body := attributedText(in["body"])
	conversationURN := requestedConversationURN
	if conversationURN == "" {
		if conversation, ok := in["conversation"].(map[string]any); ok {
			conversationURN = stringValue(conversation["entityUrn"])
		}
	}
	return map[string]any{
		"message_urn":       firstNonEmpty(stringValue(in["entityUrn"]), stringValue(in["backendUrn"])),
		"entity_urn":        stringValue(in["entityUrn"]),
		"backend_urn":       stringValue(in["backendUrn"]),
		"conversation_urn":  conversationURN,
		"sender_entity_urn": extractSenderEntityURN(in),
		"subject":           stringValue(in["subject"]),
		"body":              body,
		"body_preview":      strutil.TruncateSingleLine(body, 240),
		"delivered_at":      int64Value(in["deliveredAt"]),
		"origin_token":      stringValue(in["originToken"]),
	}
}

func extractSenderEntityURN(in map[string]any) string {
	if sender, ok := in["sender"].(map[string]any); ok {
		if value := stringValue(sender["entityUrn"]); value != "" {
			return value
		}
	}
	if actor, ok := in["actor"].(map[string]any); ok {
		if value := stringValue(actor["entityUrn"]); value != "" {
			return value
		}
	}
	return ""
}

func extractSyncToken(m map[string]any) string {
	if meta, ok := m["metadata"].(map[string]any); ok {
		if token := stringValue(meta["newSyncToken"]); token != "" {
			return token
		}
	}
	return ""
}
