package daemon

import (
	"strings"
	"time"
)

const hubAuthBackoff = 5 * time.Minute

func isHubAuthError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "hub returned html instead of json"):
		return true
	case strings.Contains(msg, "invalid character '<'"):
		return true
	case strings.Contains(msg, "fetch hub hosts failed (401)"):
		return true
	case strings.Contains(msg, "fetch hub hosts failed (403)"):
		return true
	case strings.Contains(msg, "auth required"):
		return true
	default:
		return false
	}
}
