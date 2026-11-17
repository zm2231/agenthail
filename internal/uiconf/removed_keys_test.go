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

func TestARetiredKeyIsAcceptedQuietly(t *testing.T) {
	parsed := Parse("scrim.feather = 40\nscrim.feather@mac = 80\ncontrast.target = 5\n")
	if len(parsed.Problems) != 0 {
		t.Fatalf("a retired key warned: %+v", parsed.Problems)
	}
	if _, err := ParseAssignment("scrim.feather = 40"); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Errorf("setting a retired key: %v", err)
	}
	for key := range retiredKeys {
		if _, still := Lookup(key); still {
			t.Errorf("%s is listed as retired but is still a key", key)
		}
	}
}
