package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtensionCallInputDefaultsToAnEmptyObject(t *testing.T) {
	for _, raw := range []string{"", "  "} {
		input, err := extensionCallInput(raw)
		if err != nil || string(input) != "{}" {
			t.Fatalf("no --input (%q) = %s, %v; want {}", raw, input, err)
		}
		if _, err := json.Marshal(map[string]any{"input": input}); err != nil {
			t.Fatalf("the default input does not encode: %v", err)
		}
	}
	input, err := extensionCallInput(`{"day":"today"}`)
	if err != nil || string(input) != `{"day":"today"}` {
		t.Fatalf("a JSON --input = %s, %v; want it as given", input, err)
	}
	if _, err := extensionCallInput("{day: today}"); err == nil || !strings.Contains(err.Error(), "--input must be JSON") || strings.Contains(err.Error(), "MarshalJSON") {
		t.Fatalf("a bad --input = %v; want a plain message naming --input", err)
	}
}
