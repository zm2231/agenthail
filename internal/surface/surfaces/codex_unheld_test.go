package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type idleCodexClient struct{}

func (idleCodexClient) Request(context.Context, string, map[string]any, time.Duration) (map[string]any, error) {
	return map[string]any{}, nil
}

func (idleCodexClient) Close() error { return nil }

func TestAThreadCountsAsUnheldOnlyWhenEveryRuntimeAgreesAndItIsQuiet(t *testing.T) {
	dir := t.TempDir()
	rollout := filepath.Join(dir, "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	quiet := now.Add(-time.Hour)
	if err := os.Chtimes(rollout, quiet, quiet); err != nil {
		t.Fatal(err)
	}
	sess := &surface.Session{ID: "thread-1", Surface: surface.KindCodex, Transcript: rollout}
	home := CodexHome{Codex: filepath.Join(dir, "codex"), State: filepath.Join(dir, "state")}
	withDesktop := &Codex{desktopURL: "ws://127.0.0.1:1", managed: true, home: home}
	withoutDesktop := &Codex{managed: true, home: home}
	listed := func(ids ...string) map[string]bool {
		loaded := map[string]bool{}
		for _, id := range ids {
			loaded[id] = true
		}
		return loaded
	}
	cases := []struct {
		name     string
		codex    *Codex
		runtimes *codexRuntimes
		at       time.Time
		want     bool
	}{
		{"loaded in the Desktop bridge but not the managed server", withDesktop, &codexRuntimes{desktop: idleCodexClient{}, managed: idleCodexClient{}, desktopLoaded: listed(sess.ID), managedLoaded: listed()}, now, false},
		{"loaded in the managed server", withDesktop, &codexRuntimes{desktop: idleCodexClient{}, managed: idleCodexClient{}, desktopLoaded: listed(), managedLoaded: listed(sess.ID)}, now, false},
		{"the Desktop bridge could not be reached", withDesktop, &codexRuntimes{managed: idleCodexClient{}, managedLoaded: listed()}, now, false},
		{"a runtime could not list its threads", withDesktop, &codexRuntimes{desktop: idleCodexClient{}, managed: idleCodexClient{}, desktopLoaded: listed(), managedLoaded: listed(), loadFailures: []string{"Codex Desktop: timeout"}}, now, false},
		{"held by neither but written to just now, as a codex terminal does", withDesktop, &codexRuntimes{desktop: idleCodexClient{}, managed: idleCodexClient{}, desktopLoaded: listed(), managedLoaded: listed()}, quiet.Add(time.Minute), false},
		{"held by neither and quiet", withDesktop, &codexRuntimes{desktop: idleCodexClient{}, managed: idleCodexClient{}, desktopLoaded: listed(), managedLoaded: listed()}, now, true},
		{"no Desktop here and the managed server doesn't hold it", withoutDesktop, &codexRuntimes{managed: idleCodexClient{}, managedLoaded: listed()}, now, true},
		{"no Desktop here and no managed server running", withoutDesktop, &codexRuntimes{}, now, true},
	}
	for _, tc := range cases {
		if got := tc.codex.unheld(t.Context(), tc.runtimes, sess, tc.at); got != tc.want {
			t.Errorf("%s: unheld=%v, want %v", tc.name, got, tc.want)
		}
	}
	if err := os.MkdirAll(filepath.Dir(home.socketPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.socketPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if withoutDesktop.unheld(t.Context(), &codexRuntimes{}, sess, now) {
		t.Error("a managed server whose socket exists but could not be reached might hold the thread")
	}
	if withoutDesktop.unheld(t.Context(), &codexRuntimes{managed: idleCodexClient{}, managedLoaded: listed()}, &surface.Session{ID: "thread-2", Surface: surface.KindCodex, Transcript: filepath.Join(dir, "missing.jsonl")}, now) {
		t.Error("a thread whose rollout can't be read counted as quiet")
	}
}

func TestAQuietThreadWithWorkStillOpenInItsRolloutIsNotUnheld(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	bridge := startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/loaded/list", "thread/turns/list":
			return `{"result":{"data":[]}}`
		case "thread/read":
			return `{"result":{"thread":{"id":"thread","source":"cli","status":{"type":"notLoaded"}}}}`
		}
		return ""
	})
	lines := codexBackgroundRollout()
	cases := []struct {
		name    string
		rollout []string
		want    bool
	}{
		{"a turn still running a command", lines[:2], false},
		{"an ended turn whose command still runs", lines[:4], false},
		{"an ended turn whose command finished", lines, true},
	}
	event := func(typ string) string {
		return `{"timestamp":"2026-10-26T10:00:00Z","type":"event_msg","payload":{"type":"` + typ + `","turn_id":"turn-1"}}`
	}
	for _, start := range []string{"task_started", "turn_started"} {
		cases = append(cases, struct {
			name    string
			rollout []string
			want    bool
		}{"a turn opened by " + start, []string{event(start)}, false})
		for _, end := range []string{"task_complete", "task_completed", "turn_completed", "task_cancelled", "task_canceled", "task_aborted", "turn_aborted", "interrupted"} {
			cases = append(cases, struct {
				name    string
				rollout []string
				want    bool
			}{start + " closed by " + end, []string{event(start), event(end)}, true})
		}
	}
	for _, tc := range cases {
		rollout := timelineFixture(t, strings.Join(tc.rollout, "\n")+"\n")
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(rollout, old, old); err != nil {
			t.Fatal(err)
		}
		c := inDaemon(NewCodex(bridge.URL))
		observation, err := c.Observe(t.Context(), &surface.Session{ID: "thread", Surface: surface.KindCodex, Transcript: rollout})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if observation.Unheld != tc.want {
			t.Errorf("%s: unheld=%v, want %v", tc.name, observation.Unheld, tc.want)
		}
	}
}
