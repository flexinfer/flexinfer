package backend

import (
	"os"
	"strings"
	"testing"
)

func TestSunshineHeadlessPersistsCredentials(t *testing.T) {
	script, err := os.ReadFile("../build/sunshine-headless.sh")
	if err != nil {
		t.Fatal(err)
	}

	text := string(script)
	required := []string{
		`SUNSHINE_CONFIG_HOME="${SUNSHINE_STATE_DIR}/xdg-config"`,
		`SUNSHINE_CONFIG_DIR="${SUNSHINE_CONFIG_HOME}/sunshine"`,
		`ln -s "$SUNSHINE_CONFIG_DIR" "${SUNSHINE_HOME}/.config/sunshine"`,
		`"${SUNSHINE_CONFIG_DIR}/credentials"`,
		`XDG_CONFIG_HOME="$SUNSHINE_CONFIG_HOME"`,
		`HOME="$SUNSHINE_HOME"`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("sunshine-headless.sh missing %q", want)
		}
	}
}
