package main

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/crb2nu/loom/pkg/mcperror"
)

func (s *googleWorkspaceServer) wrapGoogleError(service string, err error) error {
	if err == nil {
		return nil
	}
	if gErr, ok := err.(*googleapi.Error); ok {
		return mcperror.APIError(service, gErr.Code, gErr.Message)
	}
	if strings.Contains(strings.ToLower(err.Error()), "deadline") || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return mcperror.Timeout(service)
	}
	return mcperror.WrapAPI(service, err)
}

func validateOptionalRFC3339(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return mcperror.InvalidParam(field, "must be RFC3339")
	}
	return nil
}

func optionalStringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key]
	if !ok {
		return "", false
	}
	return strings.TrimSpace(fmt.Sprint(value)), true
}

func optionalStringSliceArg(args map[string]any, key string) ([]string, bool) {
	value, ok := args[key]
	if !ok {
		return nil, false
	}
	raw, ok := value.([]any)
	if !ok {
		typed, ok := value.([]string)
		if !ok {
			return []string{}, true
		}
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, strings.TrimSpace(item))
		}
		return result, true
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		result = append(result, strings.TrimSpace(fmt.Sprint(item)))
	}
	return result, true
}
