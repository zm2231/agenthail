package uiconf

import (
	"strings"
	"testing"
)

func TestARemovedKeyNamesItsReplacement(t *testing.T) {
	parsed := Parse("glass.sidebar.opacity = 0.4\nglass.chrome.opacity@mac = 0\nglass.bogus = 1\n")
	if len(parsed.Problems) != 3 {
		t.Fatalf("problems %+v", parsed.Problems)
	}
	for _, problem := range parsed.Problems[:2] {
		if !strings.Contains(problem.Message, "was removed; use glass.opacity") {
			t.Errorf("line %d: %q does not name the replacement", problem.Line, problem.Message)
		}
	}
	if parsed.Problems[2].Message != "unknown key glass.bogus" {
		t.Errorf("an unknown key reads %q", parsed.Problems[2].Message)
	}
	if _, err := ParseAssignment("glass.sidebar.opacity = 0.4"); err == nil || !strings.Contains(err.Error(), "use glass.opacity") {
		t.Errorf("setting a removed key: %v", err)
	}
	for key := range removedKeys {
		if _, still := Lookup(key); still {
			t.Errorf("%s is listed as removed but is still a key", key)
		}
	}
}
