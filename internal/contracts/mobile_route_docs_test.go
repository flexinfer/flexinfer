package contracts

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestMobileRoutesDocumented(t *testing.T) {
	source, err := os.ReadFile("../../internal/hud/domain/mobile/mobile.go")
	if err != nil {
		t.Fatalf("read mobile routes: %v", err)
	}
	docs, err := os.ReadFile("../../docs/MOBILE_COMPANION_API.md")
	if err != nil {
		t.Fatalf("read mobile docs: %v", err)
	}

	re := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) (/api/mobile/v1/[^"]+)"`)
	matches := re.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("no mobile routes found")
	}

	docText := string(docs)
	for _, match := range matches {
		method := match[1]
		path := match[2]
		if !strings.Contains(docText, "`"+path+"`") {
			t.Fatalf("%s %s is registered in mobile.go but absent from docs/MOBILE_COMPANION_API.md", method, path)
		}
	}
}
