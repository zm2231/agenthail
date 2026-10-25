package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestHeadlessCodexCLICreatesReadsAndDrivesAThread(t *testing.T) {
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), nil)
	r, err := registry.Open(registrytest.Path(t))
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
	if len(thread.Turns) != 2 || !strings.Contains(thread.Turns[0].User, "Mention breaking changes") || thread.Turns[0].Status != "interrupted" || thread.Turns[1].User != surface.CLILabel+"Write it again, shorter" {
		t.Fatalf("thread=%+v", thread)
	}

	lifecycle.Crash()
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "@headless", "After the daemon stopped"}) }); err != nil {
		t.Fatalf("send after the daemon stopped err=%v", err)
	}
	if thread, _ := lifecycle.Daemon().Thread(id); len(thread.Turns) != 3 || thread.Turns[2].User != surface.CLILabel+"After the daemon stopped" {
		t.Fatalf("thread=%+v", thread)
	}
	if commands := lifecycle.Commands(); len(commands) != 2 {
		t.Fatalf("lifecycle commands=%v, want a second start after the daemon stopped", commands)
	}

	lifecycle.Crash()
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:" + id, "By thread id"}) }); err != nil {
		t.Fatalf("send by qualified id after the daemon stopped err=%v", err)
	}
	if thread, _ := lifecycle.Daemon().Thread(id); len(thread.Turns) != 4 || thread.Turns[3].User != surface.CLILabel+"By thread id" {
		t.Fatalf("thread=%+v", thread)
	}
	if resumes := lifecycle.Daemon().Calls("thread/resume"); len(resumes) != 1 {
		t.Fatalf("restarted daemon resumes=%v", resumes)
	}
	if commands := lifecycle.Commands(); len(commands) != 3 {
		t.Fatalf("lifecycle commands=%v, want a third start", commands)
	}

	sibling := id[:len(id)-2] + "ff"
	if err := r.RegisterSession(surface.Session{ID: sibling, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "managed"}); err != nil {
		t.Fatal(err)
	}
	lifecycle.Crash()
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:" + id[:len(id)-2], "Ambiguous"}) }); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("send by an ambiguous prefix err=%v, want ambiguity", err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:" + id[:len(id)-1], "By prefix"}) }); err != nil {
		t.Fatalf("send by a unique prefix after the daemon stopped err=%v", err)
	}
	if thread, _ := lifecycle.Daemon().Thread(id); len(thread.Turns) != 5 || thread.Turns[4].User != surface.CLILabel+"By prefix" {
		t.Fatalf("thread=%+v", thread)
	}
	if resumes := lifecycle.Daemon().Calls("thread/resume"); len(resumes) != 1 {
		t.Fatalf("restarted daemon resumes=%v", resumes)
	}
	if commands := lifecycle.Commands(); len(commands) != 4 {
		t.Fatalf("lifecycle commands=%v, want a fourth start", commands)
	}
}
