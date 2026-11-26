package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/acphost"
	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/acpsurface"
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

func TestRootsDefaultShowsAndChangesWhereAPersonsSessionsStart(t *testing.T) {
	claudeRootsHome(t)
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	show := func() (string, claudepick.Pick) {
		t.Helper()
		output, err := captureStdout(t, func() error { return app.Run([]string{"roots", "default", "--json"}) })
		var answer struct {
			DefaultRoot string          `json:"defaultRoot"`
			Pick        claudepick.Pick `json:"pick"`
		}
		if err != nil || json.Unmarshal([]byte(output), &answer) != nil {
			t.Fatalf("output=%q err=%v", output, err)
		}
		return answer.DefaultRoot, answer.Pick
	}
	if chosen, pick := show(); chosen != "" || pick.Root != "" || pick.Name != "default" || pick.Reason != "the primary root" {
		t.Fatalf("with no default a person's session starts in the primary: default=%q pick=%+v", chosen, pick)
	}
	for _, args := range [][]string{{"nope"}, {"claude", "extra"}} {
		if err := app.Run(append([]string{"roots", "default"}, args...)); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"roots", "default", "claude"}) }); err != nil {
		t.Fatal(err)
	}
	if chosen, pick := show(); chosen != "claude" || pick.Name != "claude" || pick.Reason != "your default root for new sessions" {
		t.Fatalf("default=%q pick=%+v", chosen, pick)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"roots", "default", "none"}) }); err != nil {
		t.Fatal(err)
	}
	if chosen, _ := show(); chosen != "" {
		t.Fatalf("none clears the default: %q", chosen)
	}
}

func TestDoctorReportsAnUnreadableSettingsFile(t *testing.T) {
	claudeRootsHome(t)
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	report := func() (bool, string) {
		t.Helper()
		output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
		var answer struct {
			OK            bool   `json:"ok"`
			SettingsError string `json:"settingsError"`
		}
		if err := json.Unmarshal([]byte(output), &answer); err != nil {
			t.Fatalf("output=%s err=%v", output, err)
		}
		return answer.OK, answer.SettingsError
	}
	if _, problem := report(); problem != "" {
		t.Fatalf("no settings file is not a problem: %s", problem)
	}
	if err := os.WriteFile(dashconfig.Path(), []byte(`{not json`), 0600); err != nil {
		t.Fatal(err)
	}
	ok, problem := report()
	if ok || !strings.Contains(problem, dashconfig.Path()) || !strings.Contains(problem, "parse dashboard config") {
		t.Fatalf("doctor names the file and the parse error: ok=%v problem=%q", ok, problem)
	}
}

func TestThreadCreateStartsInTheCreatorsRootElseTheDefault(t *testing.T) {
	starter := &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}, session: &surface.Session{ID: "c1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Cwd: t.TempDir()}}
	app := threadFixture(t, starter)
	serveCreates(t, app)
	keys := claudeRootsHome(t)
	home, _ := os.UserHomeDir()
	agent := surface.Session{ID: "agent-primary", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transcript: filepath.Join(home, ".claude", "projects", "-repo", "agent-primary.jsonl")}
	if err := app.registry().RecordObservedSession(agent); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"roots", "default", "claude"}) }); err != nil {
		t.Fatal(err)
	}
	create := func(extra ...string) threadCreateOutput {
		t.Helper()
		output, err := captureStdout(t, func() error {
			return app.Run(append([]string{"thread", "create", "claude", "hello", "--cwd", starter.session.Cwd, "--json"}, extra...))
		})
		var result threadCreateOutput
		if err != nil || json.Unmarshal([]byte(output), &result) != nil {
			t.Fatalf("output=%q err=%v", output, err)
		}
		return result
	}
	if result := create(); starter.options[0].ConfigRoot != keys || result.Account == nil || result.Account.Reason != "your default root for new sessions" {
		t.Fatalf("a person's session starts in the default root: options=%+v account=%+v", starter.options[0], result.Account)
	}
	if result := create("--from", agent.ID); starter.options[1].ConfigRoot != "" || result.Account == nil || result.Account.Name != "default" || result.Account.Reason != "the root of the session that created it" {
		t.Fatalf("an agent's session starts in the agent's root, not the default: options=%+v account=%+v", starter.options[1], result.Account)
	}
	create("--root", "default")
	if starter.options[2].ConfigRoot != "default" {
		t.Fatalf("a named root is passed through untouched: %+v", starter.options[2])
	}
}

func TestDoctorReportsASkippedACPAgentEntry(t *testing.T) {
	claudeRootsHome(t)
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Dir(surface.AgenthailConfigPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(surface.AgenthailConfigPath(home), []byte(`{"acpAgents": [{"id": "claude", "command": ["x"]}, {"id": "helper", "command": ["helper-agent"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	var answer struct {
		OK       bool     `json:"ok"`
		Problems []string `json:"acpAgentProblems"`
	}
	if err := json.Unmarshal([]byte(output), &answer); err != nil {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if answer.OK || len(answer.Problems) != 1 || !strings.Contains(answer.Problems[0], `"claude"`) {
		t.Fatalf("doctor must name the skipped entry and only it: %+v", answer)
	}
}

func TestDoctorReadsABuiltInAgentThatIsNotInstalledAsUnavailableAndFailsAConfiguredOne(t *testing.T) {
	claudeRootsHome(t)
	t.Setenv("PATH", t.TempDir())
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	builtIn := acphost.BuiltInAgents()[0]
	configured := acphost.Agent{ID: "helper", Label: "Helper", Command: []string{"helper-agent"}}
	app.Surfaces = append(app.Surfaces,
		SurfaceEntry{Name: builtIn.ID, Surface: acpsurface.New(t.TempDir(), builtIn)},
		SurfaceEntry{Name: configured.ID, Surface: acpsurface.New(t.TempDir(), configured)})
	output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	var answer struct {
		Surfaces []struct {
			Surface          string `json:"surface"`
			OK               bool   `json:"ok"`
			Error            string `json:"error"`
			AgentUnavailable string `json:"agentUnavailable"`
		} `json:"surfaces"`
	}
	if err := json.Unmarshal([]byte(output), &answer); err != nil {
		t.Fatalf("output=%s err=%v", output, err)
	}
	var names []string
	for _, s := range answer.Surfaces {
		names = append(names, s.Surface)
		if s.Surface == builtIn.ID && (!s.OK || s.Error != "" || !strings.Contains(s.AgentUnavailable, "not installed") || !strings.Contains(s.AgentUnavailable, "agenthail agents use devin off")) {
			t.Fatalf("a built-in agent that is not installed reads as unavailable, not as a fault: %+v", s)
		}
		if s.Surface == configured.ID && (s.OK || !strings.Contains(s.Error, "turned on but not installed") || !strings.Contains(s.Error, `"helper-agent"`)) {
			t.Fatalf("a configured agent whose command is missing is a failure: %+v", s)
		}
	}
	if !slices.Contains(names, builtIn.ID) || !slices.Contains(names, configured.ID) {
		t.Fatalf("both agents must be reported: %v", names)
	}
}
