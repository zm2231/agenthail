package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/surface"
)

func claudeRootsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	keys := filepath.Join(home, ".keys", "claude")
	for path, content := range map[string]string{
		filepath.Join(home, ".claude.json"):              `{"oauthAccount":{"accountUuid":"a","emailAddress":"me@example.com"}}`,
		filepath.Join(home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + keys + `"]}`,
		filepath.Join(keys, "settings.json"):             `{"apiKeyHelper":"key-helper"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestRootsPolicyShowsAndChangesTheHostsChoice(t *testing.T) {
	claudeRootsHome(t)
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	show := func() (claudepick.Policy, claudepick.Pick) {
		t.Helper()
		output, err := captureStdout(t, func() error { return app.Run([]string{"roots", "policy", "--json"}) })
		var answer struct {
			Policy claudepick.Policy `json:"policy"`
			Pick   claudepick.Pick   `json:"pick"`
		}
		if err != nil || json.Unmarshal([]byte(output), &answer) != nil {
			t.Fatalf("output=%q err=%v", output, err)
		}
		return answer.Policy, answer.Pick
	}
	if policy, pick := show(); policy != claudepick.DefaultPolicy() || pick.Root != "" || pick.Reason != "no usage reading" {
		t.Fatalf("defaults pick the primary when no usage is known and never the API key: policy=%+v pick=%+v", policy, pick)
	}
	for _, args := range [][]string{{"prefer", "nope"}, {"ceilings", "0", "90"}, {"ceilings", "x", "90"}, {"auto", "maybe"}} {
		if err := app.Run(append([]string{"roots", "policy"}, args...)); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	for _, args := range [][]string{{"ceilings", "80", "95"}, {"auto", "off"}} {
		if _, err := captureStdout(t, func() error { return app.Run(append([]string{"roots", "policy"}, args...)) }); err != nil {
			t.Fatal(err)
		}
	}
	if policy, pick := show(); policy.SessionCeiling != 80 || policy.WeeklyCeiling != 95 || policy.Auto || pick.Reason != "automatic choice is off" {
		t.Fatalf("policy=%+v pick=%+v", policy, pick)
	}
}

func TestThreadCreateStartsInThePickedRootAndSaysWhy(t *testing.T) {
	keys := claudeRootsHome(t)
	starter := &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}, session: &surface.Session{ID: "c1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Cwd: t.TempDir()}}
	app := threadFixture(t, starter)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"roots", "policy", "prefer", "claude"}) }); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "claude", "hello", "--cwd", starter.session.Cwd, "--json"})
	})
	var result threadCreateOutput
	if err != nil || json.Unmarshal([]byte(output), &result) != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if starter.options[0].ConfigRoot != keys || result.Account == nil || result.Account.Name != "claude" || result.Account.Reason != "preferred; uses an API key" {
		t.Fatalf("options=%+v account=%+v", starter.options[0], result.Account)
	}
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "claude", "hello", "--cwd", starter.session.Cwd, "--root", "default", "--json"})
	}); err != nil {
		t.Fatal(err)
	}
	if starter.options[1].ConfigRoot != "default" {
		t.Fatalf("a named root is passed through untouched: %+v", starter.options[1])
	}
}
