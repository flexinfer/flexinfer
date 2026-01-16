package codebase

import (
	"strings"
	"testing"
)

func TestParseGitBlamePorcelainForLines(t *testing.T) {
	in := strings.NewReader(strings.TrimSpace(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 1 1 2\n"+
			"author Alice Example\n"+
			"\tline 1\n"+
			"\tline 2\n"+
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 3 3 1\n"+
			"author Bob Example\n"+
			"\tline 3\n",
	) + "\n")

	wanted := map[int]bool{1: true, 3: true}
	got, err := parseGitBlamePorcelainForLines(in, wanted)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if got[1].commit != "aaaaaaaa" || got[1].author != "Alice Example" {
		t.Fatalf("line 1: got commit=%q author=%q", got[1].commit, got[1].author)
	}
	if got[3].commit != "bbbbbbbb" || got[3].author != "Bob Example" {
		t.Fatalf("line 3: got commit=%q author=%q", got[3].commit, got[3].author)
	}
}
