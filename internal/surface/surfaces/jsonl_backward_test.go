package surfaces

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTheBackwardLifecycleScanHandlesRolloutEdges(t *testing.T) {
	at := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	started := codexRolloutRecord(at, "event_msg", `{"type":"task_started"}`)
	row := codexRolloutRecord(at.Add(time.Second), "event_msg", `{"type":"token_count"}`)

	cases := []struct {
		name    string
		content string
		offset  int64
		found   bool
	}{
		{"a lifecycle record at byte zero", started + "\n" + row + "\n", 0, true},
		{"CRLF line endings", row + "\r\n" + started + "\r\n" + row + "\r\n", int64(len(row) + 2), true},
		{"an unterminated last line", started + "\n" + row, 0, true},
		{"an unterminated lifecycle line", row + "\n" + started, int64(len(row) + 1), true},
		{"no lifecycle record", row + "\n" + row + "\n", 0, false},
	}
	for _, c := range cases {
		content := []byte(c.content)
		scan := lastCodexLifecycle(bytes.NewReader(content), int64(len(content)), int64(len(content)))
		if scan.found != c.found || (c.found && scan.offset != c.offset) {
			t.Fatalf("%s: found=%t offset=%d want found=%t offset=%d", c.name, scan.found, scan.offset, c.found, c.offset)
		}
		if c.found && !strings.HasPrefix(strings.TrimLeft(c.content[scan.offset:], "\r\n"), started) {
			t.Fatalf("%s: offset %d does not start the lifecycle record", c.name, scan.offset)
		}
	}
}
