package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The apps and the dashboard stop offering a tap retry under a send's saved key
// once the daemon may have dropped its receipt, which is when held replays stop too.
func TestTheAppsStopRetryingASendsKeyWhenTheDaemonStopsReplayingIt(t *testing.T) {
	for _, client := range []struct {
		path    string
		pattern string
		unit    time.Duration
	}{
		{filepath.Join("..", "..", "native", "PendingSend.swift"), `static let lifetime: TimeInterval = ([0-9* ]+)\n`, time.Second},
		{filepath.Join("dashboard", "js", "pending-send.js"), `const keyLifetime = ([0-9* ]+);`, time.Millisecond},
	} {
		source, err := os.ReadFile(client.path)
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile(client.pattern).FindSubmatch(source)
		if match == nil {
			t.Fatalf("%s has no send key lifetime", client.path)
		}
		product := int64(1)
		for _, factor := range strings.Split(string(match[1]), "*") {
			n, err := strconv.ParseInt(strings.TrimSpace(factor), 10, 64)
			if err != nil {
				t.Fatalf("%s lifetime %q: %v", client.path, match[1], err)
			}
			product *= n
		}
		if got := time.Duration(product) * client.unit; got != heldReplayLimit {
			t.Fatalf("%s keeps a send's key for %s, the daemon replays held work for %s", client.path, got, heldReplayLimit)
		}
	}
}
