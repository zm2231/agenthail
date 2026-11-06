package github

import (
	"slices"
	"testing"
)

func TestCreatedByLinksOnlyWhatTheCommandOpened(t *testing.T) {
	const url = "https://github.com/acme/app/pull/42"
	const other = "https://github.com/acme/app/pull/7"
	cases := []struct {
		name    string
		command string
		output  string
		want    []int
	}{
		{"plain create", `gh pr create --title "Fix" --body "x"`, "Creating pull request\n" + url + "\n", []int{42}},
		{"crlf output", `gh pr create --fill`, url + "\r\n", []int{42}},
		{"after cd and env", `cd app && GH_PROMPT_DISABLED=1 gh pr create --fill`, url, []int{42}},
		{"codex shell wrapper", `/bin/zsh -lc 'git push -u origin HEAD && gh pr create --fill'`, url, []int{42}},
		{"heredoc body", "gh pr create --title Fix --body \"$(cat <<'EOF'\nSee " + other + "\nEOF\n)\"", url, []int{42}},
		{"url inside a longer line", `gh pr create --fill`, "created " + url, nil},
		{"echoed text", `echo "gh pr create"`, url, nil},
		{"comment", "git push # then gh pr create", url, nil},
		{"heredoc mention only", "cat <<EOF\ngh pr create\nEOF", url, nil},
		{"subcommand runs into another word", `gh pr create#x`, url, nil},
		{"listing beside create", `gh pr list && gh pr create --fill`, url + "\n" + other, nil},
		{"api beside create", `gh pr create --fill; gh api repos/acme/app/pulls`, url, nil},
		{"more urls than creates", `gh pr create --fill`, url + "\n" + other, nil},
		{"named by another command", `gh pr create --fill || gh pr view 42`, url, nil},
		{"named in quoted text", `gh pr create --fill; printf 'https://github.com/acme/app/pull/42'`, url, nil},
		{"two creates", `gh pr create -R acme/app --fill && gh pr create -R acme/app -B next --fill`, url + "\n" + other, []int{42, 7}},
		{"not a pull url", `gh pr create --fill`, "https://github.com/acme/app\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []int
			for _, ref := range CreatedBy(c.command, c.output) {
				if ref.Repo != (Repo{Host: "github.com", Owner: "acme", Name: "app"}) {
					t.Fatalf("repo = %+v", ref.Repo)
				}
				got = append(got, ref.Number)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("CreatedBy = %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseRepoReadsRemotesAndShortForms(t *testing.T) {
	want := Repo{Host: "github.com", Owner: "acme", Name: "app"}
	for _, raw := range []string{"acme/app", "github.com/acme/app", "https://github.com/acme/app.git", "git@github.com:acme/app.git", "ssh://git@github.com/acme/app"} {
		if got, ok := ParseRepo(raw); !ok || got != want {
			t.Errorf("ParseRepo(%q) = %+v, %v", raw, got, ok)
		}
	}
	for _, raw := range []string{"app", "acme/app/extra/more", "https://github.com/acme", "git@github.com:acme"} {
		if _, ok := ParseRepo(raw); ok {
			t.Errorf("ParseRepo(%q) accepted", raw)
		}
	}
	if ref, ok := ParseURL("https://github.com/acme/app/pull/42/files"); ok {
		t.Errorf("ParseURL accepted a sub-page: %+v", ref)
	}
}
