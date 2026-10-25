package cli

import (
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestSendFlagsShareOneGrammar(t *testing.T) {
	for flag := range surface.SendValueFlags {
		args := []string{"@peer", flag, "value", "done"}
		if err := validateCommandFlags("send", args); err != nil {
			t.Errorf("%s: %v", flag, err)
		}
		if got := stripFlags(args); !slices.Equal(got, []string{"@peer", "done"}) {
			t.Errorf("stripFlags kept %v for %s", got, flag)
		}
	}
	for flag := range surface.SendBoolFlags {
		if err := validateCommandFlags("send", []string{"@peer", "done", flag}); err != nil {
			t.Errorf("%s: %v", flag, err)
		}
	}
}
