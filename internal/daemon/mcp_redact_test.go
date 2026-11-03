package daemon

import (
	"fmt"
	"regexp"
	"testing"
)

var sessionTokenPattern = regexp.MustCompile(`ahs_[A-Za-z0-9_-]+`)

func redacted(value any) string {
	return sessionTokenPattern.ReplaceAllString(fmt.Sprint(value), "ahs_<redacted>")
}

func TestRedactedHidesSessionTokens(t *testing.T) {
	if got := redacted(map[string]any{"Authorization": "Bearer ahs_abc-DEF_123"}); got != "map[Authorization:Bearer ahs_<redacted>]" {
		t.Fatalf("got %s", got)
	}
}
