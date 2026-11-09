package triggers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trigger.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func completion() Completion {
	return Completion{HostID: "h1", HostName: "box", SessionID: "s1", Alias: "builder", Surface: "codex", TurnID: "t1", Outcome: "finished", ProjectID: "repo:github.com/me/site", CompletedAt: time.Unix(1700000000, 0)}
}

func TestARunTriggerGetsItsArgumentsVerbatimAndTheCompletionOnStdin(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	run := script(t, `printf '%s\n' "$@" > "$OUT"; cat >> "$OUT"; printf '%s' "$AGENTHAIL_DEDUPE_KEY" >> "$OUT"`)
	trigger := Trigger{Name: "echo", Run: []string{run, "$(touch pwned); rm -rf ~", "a b"}, Timeout: 5 * time.Second, Env: map[string]string{"OUT": out}}
	work := t.TempDir()
	result := Runner{WorkDir: work}.Execute(context.Background(), trigger, completion(), false)
	if result.State != StateSucceeded {
		t.Fatalf("%+v", result)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(data), "\n", 3)
	if lines[0] != "$(touch pwned); rm -rf ~" || lines[1] != "a b" {
		t.Fatalf("argv=%q", lines[:2])
	}
	rest := lines[2]
	document, key := rest[:strings.LastIndex(rest, "}")+1], rest[strings.LastIndex(rest, "}")+1:]
	var got map[string]any
	if err := json.Unmarshal([]byte(document), &got); err != nil {
		t.Fatalf("stdin %q: %v", document, err)
	}
	if got["dedupeKey"] != "h1:s1:t1" || got["outcome"] != "finished" || got["trigger"] != "echo" || key != "h1:s1:t1" {
		t.Fatalf("stdin=%v key=%q", got, key)
	}
	if _, err := os.Stat(filepath.Join(work, "pwned")); err == nil {
		t.Fatal("an argument ran through a shell")
	}
}

func TestATriggerPastItsTimeoutHasItsWholeProcessGroupKilled(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child")
	run := script(t, `trap '' TERM; (trap '' TERM; echo $$ > "$PIDFILE"; exec sleep 60) & sleep 60`)
	trigger := Trigger{Name: "slow", Run: []string{run}, Timeout: time.Second, Env: map[string]string{"PIDFILE": pidFile}}
	start := time.Now()
	result := Runner{WorkDir: t.TempDir(), Grace: 200 * time.Millisecond}.Execute(context.Background(), trigger, completion(), false)
	if result.State != StateTimedOut || time.Since(start) > 10*time.Second {
		t.Fatalf("%+v after %s", result, time.Since(start))
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(string(data)), &pid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("child %d that ignored SIGTERM outlived the timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAFailingTriggerKeepsItsExitCodeAndOutputTail(t *testing.T) {
	run := script(t, `echo starting; echo broken >&2; exit 3`)
	result := Runner{WorkDir: t.TempDir()}.Execute(context.Background(), Trigger{Name: "fails", Run: []string{run}, Timeout: 5 * time.Second}, completion(), false)
	if result.State != StateFailed || result.ExitCode != 3 || result.Stdout != "starting\n" || result.Stderr != "broken\n" {
		t.Fatalf("%+v", result)
	}
}

func TestOutputTailsStayWithinTheirCapAfterInvalidUTF8IsReplaced(t *testing.T) {
	run := script(t, `i=0; while [ $i -lt 6000 ]; do printf '\377a'; printf '\377a' >&2; i=$((i+1)); done`)
	result := Runner{WorkDir: t.TempDir()}.Execute(context.Background(), Trigger{Name: "noisy", Run: []string{run}, Timeout: 20 * time.Second}, completion(), false)
	if result.State != StateSucceeded || len(result.Stdout) > OutputTailBytes || len(result.Stderr) > OutputTailBytes || !utf8.ValidString(result.Stdout) || !strings.HasSuffix(result.Stdout, "a") {
		t.Fatalf("state=%s stdout=%d stderr=%d", result.State, len(result.Stdout), len(result.Stderr))
	}
}

func TestTouchCreatesAPrivateFileAndRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "done")
	if result := (Runner{}).Execute(context.Background(), Trigger{Name: "flag", Touch: path}, completion(), false); result.State != StateSucceeded {
		t.Fatalf("%+v", result)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("info=%v err=%v", info, err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1000, 0)
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if result := (Runner{}).Execute(context.Background(), Trigger{Name: "flag", Touch: link}, completion(), false); result.State != StateFailed {
		t.Fatalf("followed a symlink: %+v", result)
	}
	if info, _ := os.Stat(target); !info.ModTime().Equal(old) {
		t.Fatalf("the symlink's target was touched: %v", info.ModTime())
	}
}

func TestAMalformedTriggerFileLoadsNothing(t *testing.T) {
	executable := script(t, "exit 0")
	writable := script(t, "exit 0")
	if err := os.Chmod(writable, 0o770); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"bad json":            `{"version":1,"triggers":[`,
		"unknown field":       `{"version":1,"triggers":[{"name":"a","touch":"/tmp/x","shell":"sh"}]}`,
		"duplicate name":      `{"version":1,"triggers":[{"name":"a","touch":"/tmp/x"},{"name":"a","touch":"/tmp/y"}]}`,
		"relative run":        `{"version":1,"triggers":[{"name":"a","run":["make"]}]}`,
		"relative touch":      `{"version":1,"triggers":[{"name":"a","touch":"done"}]}`,
		"run and touch":       `{"version":1,"triggers":[{"name":"a","run":["` + executable + `"],"touch":"/tmp/x"}]}`,
		"neither":             `{"version":1,"triggers":[{"name":"a"}]}`,
		"writable program":    `{"version":1,"triggers":[{"name":"a","run":["` + writable + `"]}]}`,
		"bad outcome":         `{"version":1,"triggers":[{"name":"a","touch":"/tmp/x","when":{"outcome":["done"]}}]}`,
		"timeout too long":    `{"version":1,"triggers":[{"name":"a","touch":"/tmp/x","timeoutSeconds":601}]}`,
		"reserved env":        `{"version":1,"triggers":[{"name":"a","touch":"/tmp/x","env":{"AGENTHAIL_TRIGGER":"x"}}]}`,
		"unsupported version": `{"version":2,"triggers":[]}`,
		"bad name":            `{"version":1,"triggers":[{"name":"A B","touch":"/tmp/x"}]}`,
		"second document":     `{"version":1,"triggers":[{"name":"a","touch":"/tmp/x"}]} {"version":1}`,
		"trailing bytes":      `{"version":1,"triggers":[{"name":"a","touch":"/tmp/x"}]} x`,
	} {
		path := filepath.Join(t.TempDir(), "triggers.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if config, err := Load(path); err == nil {
			t.Errorf("%s: loaded %+v", name, config)
		}
	}
	path := filepath.Join(t.TempDir(), "triggers.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"triggers":[{"name":"a","touch":"/tmp/x"}]}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("a group-readable file: %v", err)
	}
	if config, err := Load(filepath.Join(t.TempDir(), "missing.json")); err != nil || len(config.Triggers) != 0 {
		t.Fatalf("no file: %+v %v", config, err)
	}
}

func TestATriggerMatchesOnlyTheCompletionsItNames(t *testing.T) {
	c := completion()
	for name, test := range map[string]struct {
		trigger Trigger
		want    bool
	}{
		"no filter":        {Trigger{}, true},
		"outcome":          {Trigger{When: When{Outcome: []string{"failed"}}}, false},
		"project":          {Trigger{When: When{Project: []string{"repo:github.com/me/site"}}}, true},
		"surface":          {Trigger{When: When{Surface: []string{"claude"}}}, false},
		"session by alias": {Trigger{When: When{Session: []string{"@builder"}}}, true},
		"session by id":    {Trigger{When: When{Session: []string{"s1"}}}, true},
		"another session":  {Trigger{When: When{Session: []string{"@other"}}}, false},
	} {
		if got := test.trigger.Matches(c); got != test.want {
			t.Errorf("%s: %v", name, got)
		}
	}
	c.Delegated = true
	if (Trigger{}).Matches(c) || !(Trigger{Delegated: true}).Matches(c) {
		t.Fatal("a delegated session must fire only triggers that opt in")
	}
}

func TestThePayloadStaysWithinItsCap(t *testing.T) {
	c := completion()
	c.Cwd, c.Name = strings.Repeat("\x01", 20000), strings.Repeat("é", 5000)
	data := Payload(c, "big", false)
	var got map[string]any
	if len(data) > MaxPayloadBytes || json.Unmarshal(data, &got) != nil || got["truncated"] != true {
		t.Fatalf("len=%d truncated=%v", len(data), got["truncated"])
	}
}
