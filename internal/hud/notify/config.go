package notify

import (
	"os"
	"strings"
)

const desktopNotificationsEnv = "LOOM_HUD_DESKTOP_NOTIFICATIONS"

func desktopNotificationsEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(desktopNotificationsEnv)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
