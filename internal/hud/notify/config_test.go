package notify

import "testing"

func TestDesktopNotificationsEnabled(t *testing.T) {
	t.Setenv(desktopNotificationsEnv, "")
	if desktopNotificationsEnabled() {
		t.Fatal("expected desktop notifications to be disabled by default")
	}

	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv(desktopNotificationsEnv, value)
		if !desktopNotificationsEnabled() {
			t.Fatalf("expected %q to enable desktop notifications", value)
		}
	}

	for _, value := range []string{"0", "false", "off", "no", "garbage"} {
		t.Setenv(desktopNotificationsEnv, value)
		if desktopNotificationsEnabled() {
			t.Fatalf("expected %q to disable desktop notifications", value)
		}
	}
}
