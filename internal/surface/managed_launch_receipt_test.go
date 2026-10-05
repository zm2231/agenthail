package surface

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedCodexLaunchReceiptRoundTripsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch.json")
	receipt := ManagedCodexLaunchReceipt{LaunchID: "agenthail-1", ThreadID: "thread-1", Cwd: "/work", Runtime: LauncherTMUX, TmuxSession: "agenthail-1", TmuxPane: "%1"}
	if err := WriteManagedCodexLaunchReceipt(path, receipt); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManagedCodexLaunchReceipt(path)
	if err != nil || got != receipt {
		t.Fatalf("receipt=%+v err=%v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("receipt mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestReadManagedCodexLaunchReceiptRejectsMalformedAndOversizedFiles(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "malformed", data: "{"},
		{name: "oversized", data: fmt.Sprintf("%q", string(make([]byte, 1<<20)))},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "launch.json")
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadManagedCodexLaunchReceipt(path); err == nil {
				t.Fatal("ReadManagedCodexLaunchReceipt accepted invalid receipt")
			}
		})
	}
}

func TestReclaimCMUXKeepsReceiptWhenInventoryCannotBeVerified(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := ManagedCodexLaunchReceiptPath("agenthail-cmux")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedCodexLaunchReceipt(path, ManagedCodexLaunchReceipt{LaunchID: "agenthail-cmux", ThreadID: "thread", Cwd: "/work", Runtime: LauncherCMUX, Workspace: "workspace:7", Surface: "surface:9"}); err != nil {
		t.Fatal(err)
	}
	if err := ReclaimManagedCodexCMUXLaunchReceipts(func(string, string) (bool, error) {
		return false, errors.New("invalid inventory")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("receipt was reclaimed after unverifiable inventory: %v", err)
	}
}
