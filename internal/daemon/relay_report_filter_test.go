package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/report"
)

const (
	relayAnyReport       = "(?:\\A|\\n)`{3}agenthail-report\\n\\{[^\\n]*\\}\\n`{3}\\s*\\z"
	relayDoneReport      = "(?:\\A|\\n)`{3}agenthail-report\\n\\{[^\\n]*\"status\" *: *\"done\"[^\\n]*\\}\\n`{3}\\s*\\z"
	relayAttentionReport = "(?:\\A|\\n)`{3}agenthail-report\\n\\{[^\\n]*\"status\" *: *\"(?:blocked|failed|partial)\"[^\\n]*\\}\\n`{3}\\s*\\z"
)

func TestRelayReportFiltersMatchOnlyAReportThatEndsTheReply(t *testing.T) {
	block := func(line string) string { return "```agenthail-report\n" + line + "\n```" }
	done := block(`{"v":1,"status":"done","stop":"complete","next":"Nothing to do"}`)
	blocked := block(`{"v":1,"status":"blocked","stop":"needsInput","reason":"deploy-token.txt was not found in the folder"}`)
	cases := []struct {
		name                 string
		text                 string
		any, done, attention bool
	}{
		{"done report ends the reply", "I checked that the folder is empty.\n\n" + done, true, true, false},
		{"reply is only a blocked report", blocked, true, false, true},
		{"report spaced and followed by whitespace", "Done.\n" + block(`{"v": 1, "status": "partial", "stop": "gaveUp", "reason": "Ran out of time"}`) + "\n  \n", true, false, true},
		{"failed report", "Stopped.\n\n" + block(`{"v":1,"status":"failed","stop":"gaveUp","reason":"The build server is down"}`), true, false, true},
		{"escaped status inside a blocked reason", "Stuck.\n\n" + block(`{"v":1,"status":"blocked","stop":"needsInput","reason":"the log said \"status\":\"done\" earlier"}`), true, false, true},
		{"report quoted mid-reply", "An example of that format:\n\n" + done + "\n\nThe fields describe the real state of the task.", false, false, false},
		{"report inside a longer fence at the end", "To show the format:\n\n````markdown\n" + done + "\n````", false, false, false},
		{"report inside a tilde fence at the end", "To show the format:\n\n~~~\n" + done + "\n~~~", false, false, false},
		{"indented report at the end", "To show the format:\n\n    ```agenthail-report\n    {\"v\":1,\"status\":\"done\",\"stop\":\"complete\"}\n    ```", false, false, false},
		{"fence opened mid-line", "Done. " + done, false, false, false},
		{"report spread over lines", "Done.\n\n```agenthail-report\n{\"v\":1,\n\"status\":\"done\",\"stop\":\"complete\"}\n```", false, false, false},
		{"prose naming the report", "Workers end with an agenthail-report block whose status is done.", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, filter := range []struct {
				pattern string
				want    bool
			}{{relayAnyReport, c.any}, {relayDoneReport, c.done}, {relayAttentionReport, c.attention}} {
				if got := matchPattern(filter.pattern, c.text); got != filter.want {
					t.Errorf("matchPattern(%s) = %v, want %v", filter.pattern, got, filter.want)
				}
			}
			_, err := report.Parse(c.text)
			if c.any && errors.Is(err, report.ErrAbsent) {
				t.Errorf("the relay filter matches a reply where report.Parse finds no report")
			}
			if err == nil && !c.any {
				t.Errorf("report.Parse reads a report the relay filter does not match")
			}
		})
	}
}

func TestRelayReportFiltersAreTheOnesTheDocsGive(t *testing.T) {
	quoted := func(pattern string) string { return "'" + pattern + "'" }
	for _, doc := range []struct {
		path    string
		filters []string
	}{
		{"README.md", []string{relayDoneReport, relayAttentionReport}},
		{filepath.Join("skills", "agenthail-operations", "SKILL.md"), []string{relayAnyReport, relayDoneReport, relayAttentionReport}},
	} {
		text, err := os.ReadFile(filepath.Join("..", "..", doc.path))
		if err != nil {
			t.Fatal(err)
		}
		for _, filter := range doc.filters {
			if !strings.Contains(string(text), quoted(filter)) {
				t.Errorf("%s does not give the relay filter %s", doc.path, quoted(filter))
			}
		}
	}
}
