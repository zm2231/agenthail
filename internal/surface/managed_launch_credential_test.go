package surface

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func launchCredentialRun(t *testing.T, outcome error) (*processLauncher, *string) {
	t.Helper()
	launcher := newTMUX().(*processLauncher)
	session := new(string)
	launcher.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		index := slices.Index(args, "-s")
		if index < 0 || index+1 >= len(args) {
			t.Fatalf("tmux session argument missing: %#v", args)
		}
		*session = args[index+1]
		path, err := ManagedCodexLaunchCredentialPath(*session)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("credential file before tmux runs: info=%v err=%v", info, err)
		}
		if outcome != nil {
			return nil, outcome
		}
		return []byte(*session + " %1"), nil
	}
	return launcher, session
}

func TestATerminalCodexLaunchHandsItsCredentialToTheCLIOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	launcher, session := launchCredentialRun(t, nil)
	credential := &LaunchMCPCredential{URL: "http://127.0.0.1:7412/mcp", Token: "ahs_token", Ref: "ref-1"}
	if _, err := launcher.Launch(context.Background(), LaunchRequest{Surface: KindCodex, Options: SessionStartOptions{Cwd: "/work", Message: "hello"}, MCP: credential}); err != nil {
		t.Fatal(err)
	}
	taken, err := TakeManagedCodexLaunchCredential(*session)
	if err != nil || taken == nil || *taken != *credential {
		t.Fatalf("taken=%v err=%v", taken != nil, err)
	}
	if again, err := TakeManagedCodexLaunchCredential(*session); err != nil || again != nil {
		t.Fatalf("second take=%+v err=%v", again, err)
	}
}

func TestALaunchThatNeverStartsRemovesItsCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	launcher, session := launchCredentialRun(t, &exec.Error{Name: "tmux", Err: os.ErrNotExist})
	_, err := launcher.Launch(context.Background(), LaunchRequest{Surface: KindCodex, Options: SessionStartOptions{Cwd: "/work", Message: "hello"}, MCP: &LaunchMCPCredential{URL: "u", Token: "t", Ref: "r"}})
	var accepted LaunchAcceptedError
	if err == nil || errors.As(err, &accepted) {
		t.Fatalf("err=%v, want a definite pre-start failure", err)
	}
	path, _ := ManagedCodexLaunchCredentialPath(*session)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential file left behind: %v", err)
	}
}

func TestLaunchCredentialFilesNobodyTookAreSweptOnceTheirLaunchExpires(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old, err := writeManagedCodexLaunchCredential("agenthail-old", &LaunchMCPCredential{URL: "u", Token: "t", Ref: "r"})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := writeManagedCodexLaunchCredential("agenthail-fresh", &LaunchMCPCredential{URL: "u", Token: "t", Ref: "r"})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := PruneManagedCodexLaunchCredentials(time.Now().Add(-5 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired launch file kept: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("live launch file removed: %v", err)
	}
}

func TestALaunchCredentialThatIsNotPrivateIsRefusedAndRemoved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := ManagedCodexLaunchCredentialPath("agenthail-open")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"url":"u","token":"t","ref":"r"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if taken, err := TakeManagedCodexLaunchCredential("agenthail-open"); err == nil || taken != nil {
		t.Fatalf("taken=%v err=%v", taken != nil, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused credential left behind: %v", err)
	}
}
