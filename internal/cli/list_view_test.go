package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestListRendersDaemonPagesAndMembers(t *testing.T) {
	calls := 0
	app := snapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/snapshot" || q.Get("scope") != "all" {
			t.Errorf("request=%s", r.URL)
			http.Error(w, "bad request", 400)
			return
		}
		view := `"view":{"catalogHostId":"host","revision":"r1","scope":"all","counts":{"all":{"families":2,"sessions":3}},"projects":[],"families":`
		row := func(id, label string) string {
			child := ""
			if id == "child" {
				child = `,"subagent":{"parentId":"root","nickname":"Ada"}`
			}
			return fmt.Sprintf(`{"id":%q,"name":%q,"status":"idle","surface":"codex","alias":"builder","holders":[{"pid":101,"tty":"ttys001"},{"pid":202,"tty":"ttys002"}],"host":{"name":"vps"}%s,"display":{"kind":"waiting","label":%q,"detail":"Daemon detail"}}`, id, id, child, label)
		}
		switch {
		case q.Has("sessionId"):
			if q.Get("revision") != "r1" || q.Get("sessionId") != "child" {
				t.Errorf("hydrate=%s", r.URL)
			}
			fmt.Fprintf(w, `{%s[],"summary":{}},"sessions":[%s]}`, view, row("child", "Sent child label"))
		case q.Get("cursor") == "page2":
			fmt.Fprintf(w, `{%s[{"id":"z","rootId":"z","members":[{"id":"z"}]}]},"sessions":[%s]}`, view, row("z", "Sent second label"))
		default:
			fmt.Fprintf(w, `{%s[{"id":"root","rootId":"root","members":[{"id":"root"},{"id":"child","depth":1,"pinned":true}]}]},"sessions":[%s],"nextCursor":"page2"}`, view, row("root", "Sent first label"))
		}
	})
	human, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 conversations, 3 sessions", "vps", "Sent first label", "Sent child label", "Daemon detail", "TERMINALS", "101 ttys001, 202 ttys002", "@builder root", "@builder Ada"} {
		if !strings.Contains(human, want) {
			t.Fatalf("missing %q in %s", want, human)
		}
	}
	if strings.Index(human, "Sent child label") > strings.Index(human, "Sent second label") {
		t.Fatal(human)
	}
	raw, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all", "--json"}) })
	var state daemon.SessionViewSnapshot
	if err != nil || json.Unmarshal([]byte(raw), &state) != nil || len(state.Sessions) != 3 || state.Sessions[1].ID != "child" || state.NextCursor != "" || calls != 6 {
		t.Fatalf("output=%s calls=%d err=%v", raw, calls, err)
	}
}

func TestListRequiresDaemonContractAndNeverDiscovers(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{{ID: "raw-history"}}}
	app, store := cliFixture(t, fake)
	if err := store.RegisterSession(surface.Session{ID: "raw-history", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"list"}, {"list", "--all", "--json"}} {
		out, err := captureStdout(t, func() error { return app.Run(args) })
		if err == nil || !strings.Contains(err.Error(), "daemon is not running") || out != "" || fake.listCalls != 0 {
			t.Fatalf("out=%s err=%v calls=%d", out, err, fake.listCalls)
		}
	}
	for _, body := range []string{`{"sessions":[]}`, `{"view":{"revision":"r"},"sessions":[{"id":"x"}]}`, `{"view":{"revision":"r","families":[{"members":[{"id":"missing"}]}]},"sessions":[]}`} {
		app := snapshotFixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		out, err := captureStdout(t, func() error { return app.Run([]string{"list", "--json"}) })
		if err == nil || out != "" {
			t.Fatalf("body=%s out=%s err=%v", body, out, err)
		}
	}
}

func TestListSendsScopesAndRejectsConflicts(t *testing.T) {
	for _, scope := range []string{"current", "recent", "running", "all", "archived"} {
		app := snapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("scope"); got != scope {
				t.Errorf("scope=%s want=%s", got, scope)
			}
			fmt.Fprintf(w, `{"view":{"revision":"r","scope":%q,"counts":{%q:{"families":0,"sessions":0}},"families":[]},"sessions":[]}`, scope, scope)
		})
		args := []string{"list", "--json"}
		if scope != "current" {
			args = append(args, "--"+scope)
		}
		if _, err := captureStdout(t, func() error { return app.Run(args) }); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"list", "--all", "--recent"}, {"list", "--project", "x", "--cwd", "/x"}} {
		if err := (&App{}).Run(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestListFormatsDisplayInstantsInLocalTime(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("TZ", "Etc/GMT-2")
	at := time.Date(2026, 10, 25, 12, 30, 0, 0, time.UTC)
	got, err := renderDisplayDetail(daemon.SessionDisplay{Detail: "Automatic resume scheduled", ResumesAt: &at})
	if err != nil || !strings.Contains(got, "10/25/26 14:30:00") || strings.Contains(got, "UTC") {
		t.Fatalf("detail=%q err=%v", got, err)
	}
}

func TestListResolvesProjectFromCanonicalAll(t *testing.T) {
	app := snapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("scope") != "all" {
			t.Errorf("scope=%s", q.Get("scope"))
		}
		if q.Get("projectId") == "" {
			fmt.Fprint(w, `{"view":{"revision":"r","projects":[{"id":"retired-project","name":"Retired"}]}}`)
			return
		}
		if q.Get("projectId") != "retired-project" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"view":{"revision":"r","scope":"all","counts":{"all":{"families":0,"sessions":0}},"families":[]},"sessions":[]}`)
	})
	if _, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all", "--project", "Retired", "--json"}) }); err != nil {
		t.Fatal(err)
	}
}

func TestListCwdSendsResolvedProject(t *testing.T) {
	root := t.TempDir()
	app := snapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("projectId") != "project-folder" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"view":{"revision":"r","scope":"all","counts":{"all":{"families":0,"sessions":0}},"families":[]},"sessions":[]}`)
	})
	if err := app.Registry.AddProject(registry.ProjectRecord{ID: "project-folder", WorkspaceRoot: root, Title: "Folder"}); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all", "--cwd", root, "--json"}) }); err != nil {
		t.Fatal(err)
	}
}
