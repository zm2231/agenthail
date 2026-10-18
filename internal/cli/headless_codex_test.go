package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestHeadlessCodexCLICreatesListsReadsAndDrivesAThread(t *testing.T) {
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), nil)
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	app := &App{Registry: r, Surfaces: []SurfaceEntry{{Name: "codex", Surface: surfaces.NewCodex("1")}}, DefaultTimeout: 10 * time.Second, catalogDaemonRunning: func() bool { return false }}

	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Draft the changelog", "--cwd", t.TempDir(), "--alias", "headless", "--json"})
	})
	var created threadCreateOutput
	if err != nil || json.Unmarshal([]byte(output), &created) != nil || !created.OK || created.Session == nil || created.Session.Transport != "managed" {
		t.Fatalf("create output=%s err=%v", output, err)
	}
	id := created.Session.ID
	if commands := lifecycle.Commands(); len(commands) != 1 || commands[0] != "app-server daemon start" {
		t.Fatalf("lifecycle commands=%v", commands)
	}

	if output, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all"}) }); err != nil || !strings.Contains(output, "@headless") || !strings.Contains(output, "Draft the changelog") {
		t.Fatalf("list output=%q err=%v", output, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"steer", "@headless", "Mention breaking changes"}) }); err != nil {
		t.Fatalf("steer err=%v", err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"interrupt", "@headless"}) }); err != nil {
		t.Fatalf("interrupt err=%v", err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "@headless", "Write it again, shorter"}) }); err != nil {
		t.Fatalf("send err=%v", err)
	}
	lifecycle.Daemon().CompleteTurn(id, "Changelog drafted")
	output, err = captureStdout(t, func() error { return app.Run([]string{"reply", "@headless", "--json"}) })
	if err != nil || !strings.Contains(output, "Changelog drafted") {
		t.Fatalf("reply output=%q err=%v", output, err)
	}
	thread, _ := lifecycle.Daemon().Thread(id)
	if len(thread.Turns) != 2 || !strings.Contains(thread.Turns[0].User, "Mention breaking changes") || thread.Turns[0].Status != "interrupted" || thread.Turns[1].User != "Write it again, shorter" {
		t.Fatalf("thread=%+v", thread)
	}
}
