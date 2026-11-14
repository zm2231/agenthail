//go:build linux || darwin

package peerproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestTheKernelListsTheFilesAnotherProcessHoldsOpen(t *testing.T) {
	dir, err := os.MkdirTemp("", "files")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "mine.lock")
	held, err := os.Create(mine)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if files, err := OpenFiles(os.Getpid()); err != nil || !slices.Contains(files, mine) {
		t.Fatalf("OpenFiles(self) = %v, %v; want %s among them", files, err, mine)
	}
	theirs, ready := filepath.Join(dir, "theirs.lock"), filepath.Join(dir, "ready")
	child := exec.Command("sh", "-c", `exec 3>"$0"; : >"$1"; exec sleep 30`, theirs, ready)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never opened its file")
		}
	}
	files, err := OpenFiles(child.Process.Pid)
	if err != nil || !slices.Contains(files, theirs) || slices.Contains(files, mine) {
		t.Fatalf("OpenFiles(child) = %v, %v; want %s and not %s", files, err, theirs, mine)
	}
}
