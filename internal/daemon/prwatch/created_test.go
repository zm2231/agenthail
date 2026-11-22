package prwatch

import (
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type bodies struct {
	Host
	full map[string]string
}

func (b bodies) TimelineText(_ string, item surface.TimelineItem) string {
	if body, ok := b.full[item.BodyRef]; ok && item.Truncated {
		return body
	}
	return item.Text
}

func created(full map[string]string, items []surface.TimelineItem) []int {
	var numbers []int
	for _, ref := range New(nil, bodies{full: full}, nil).createdPRs("to", items) {
		numbers = append(numbers, ref.Number)
	}
	return numbers
}

func TestCreatedPullRequestsComeFromShellCallsWhoseOutputPrintsThem(t *testing.T) {
	url := "https://github.com/acme/app/pull/42"
	items := []surface.TimelineItem{
		{Kind: "toolCall", Title: "Bash", CallID: "c1", Text: "{\n  \"command\": \"git push -u origin HEAD && gh pr create --fill\",\n  \"description\": \"Open the pull request\"\n}"},
		{Kind: "toolResult", CallID: "c1", Text: url + "\n"},
		{Kind: "toolCall", Title: "exec_command", CallID: "c2", Text: `{"cmd":"gh pr create --fill","workdir":"/w"}`},
		{Kind: "toolResult", CallID: "c2", Status: "error", Text: "https://github.com/acme/app/pull/43\n"},
		{Kind: "toolCall", Title: "exec_command", CallID: "c3", Text: `{"cmd":"gh pr create -B next --fill","workdir":"/w"}`},
		{Kind: "toolResult", CallID: "c3", Text: "https://github.com/acme/app/pull/44\n"},
		{Kind: "toolCall", Title: "Write", CallID: "c4", Text: `{"file_path":"notes.md","content":"gh pr create"}`},
		{Kind: "toolResult", CallID: "c4", Text: "https://github.com/acme/app/pull/45\n"},
	}
	if numbers := created(nil, items); !slices.Equal(numbers, []int{42, 44}) {
		t.Fatalf("created = %v, want the Claude and Codex shell calls that succeeded", numbers)
	}
}

func TestACreatedPullRequestIsFoundPastAClippedCommandAndOutput(t *testing.T) {
	notes := strings.Repeat("a long release note line\n", 1200)
	command := `{"command":"cat > body.md <<'EOF'\n` + strings.ReplaceAll(notes, "\n", `\n`) + `EOF\ngh pr create -F body.md --fill"}`
	output := strings.Repeat("remote: building\n", 20000) + "https://github.com/acme/app/pull/42\n"
	items := []surface.TimelineItem{
		{Kind: "toolCall", Title: "Bash", CallID: "c1", Text: command[:16<<10], Truncated: true, BodyRef: "call-body"},
		{Kind: "toolResult", CallID: "c1", Text: output[:16<<10], Truncated: true, BodyRef: "result-body"},
	}
	if numbers := created(map[string]string{"call-body": command, "result-body": output}, items); !slices.Equal(numbers, []int{42}) {
		t.Fatalf("created = %v, want the pull request printed after %d bytes", numbers, len(output))
	}
}
