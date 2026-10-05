package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestBoundedNotificationTextFlattensAndLimits(t *testing.T) {
	got := boundedNotificationText("say\nhi\rnext", 10)
	if got != "say hi ne…" {
		t.Fatalf("bounded=%q", got)
	}
}

func TestDisabledNotificationsAreNoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Notify("Agenthail", strings.Repeat("x", 10), ""); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyPassesSessionToHelper(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("desktop notifications are macOS only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	argsPath := filepath.Join(home, "args")
	helper := filepath.Join(home, "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+argsPath+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_MAC_APP", helper)
	if err := SaveNotificationConfig(NotificationConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		sessionID string
		want      bool
	}{{"session-1", true}, {"", false}} {
		if err := Notify("Agenthail", "done", test.sessionID); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(data), "--session\nsession-1\n"); got != test.want {
			t.Fatalf("session %q args=%q", test.sessionID, data)
		}
		if !test.want && strings.Contains(string(data), "--session") {
			t.Fatalf("unexpected session flag: %q", data)
		}
	}
}

func TestEnableNotificationsPersistsOnlyAuthorizedState(t *testing.T) {
	home := t.TempDir()
	helper := filepath.Join(home, "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s\\n' '{\"available\":true,\"authorization\":\"authorized\",\"authorized\":true,\"alerts\":true,\"sounds\":true}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("AGENTHAIL_MAC_APP", helper)
	status, err := EnableNotifications()
	if err != nil || !status.Enabled {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	config, err := LoadNotificationConfig()
	if err != nil || !config.Enabled {
		t.Fatalf("config=%+v", config)
	}
}

func TestNotificationConfigConcurrentWritesRemainValid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(enabled bool) {
			defer group.Done()
			if err := SaveNotificationConfig(NotificationConfig{Enabled: enabled}); err != nil {
				t.Errorf("save: %v", err)
			}
		}(index%2 == 0)
	}
	group.Wait()
	if _, err := LoadNotificationConfig(); err != nil {
		t.Fatal(err)
	}
}
