package daemon

import (
	"strings"
	"testing"
)

func TestAppleScriptStringEscapesAndFlattensText(t *testing.T) {
	got := appleScriptString("say \"hi\"\\now")
	if got != `"say \"hi\"\\now"` {
		t.Fatalf("escaped=%q", got)
	}
}

func TestDisabledNotificationsAreNoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Notify("Agenthail", strings.Repeat("x", 10)); err != nil {
		t.Fatal(err)
	}
}
