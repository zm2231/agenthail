package claudepeer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/procstart"
	"github.com/zm2231/agenthail/internal/surface"
)

// Reconcile retires manifest-backed orphan workers and removes dead, same-user
// Agenthail artifacts. Foreign or ambiguous endpoints are preserved.
func Reconcile(home, socketDir string) error {
	if socketDir == "" {
		socketDir = defaultSocketDir
	}
	root := RuntimeRoot(home)
	entries, err := filepath.Glob(filepath.Join(root, "*", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range entries {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var manifest OwnershipManifest
		if json.Unmarshal(data, &manifest) != nil || !validManifest(root, socketDir, path, manifest) {
			continue
		}
		if processOwnsManifest(manifest) && processIsPeerWorker(manifest.PID) {
			_ = syscall.Kill(manifest.PID, syscall.SIGTERM)
			deadline := time.Now().Add(time.Second)
			for processOwnsManifest(manifest) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if processOwnsManifest(manifest) {
				_ = syscall.Kill(manifest.PID, syscall.SIGKILL)
			}
		} else if processExists(manifest.PID) {
			continue
		}
		if processExists(manifest.PID) {
			continue
		}
		removeManifestSocket(manifest.ControlPath, manifest.ControlDevice, manifest.ControlInode)
		removeManifestSocket(manifest.SocketPath, manifest.SocketDevice, manifest.SocketInode)
		removeOwnedRecord(manifest.RecordPath, manifest)
		cleanupManifest(path, manifest)
		_ = os.Remove(filepath.Dir(path))
	}
	return nil
}

func validManifest(root, socketDir, path string, manifest OwnershipManifest) bool {
	if (manifest.Agenthail != "peer-worker" && manifest.Agenthail != "peer-relay") || manifest.PID <= 0 || manifest.Generation == "" || manifest.ProcStart == "" || (manifest.State != "starting" && manifest.State != "ready") {
		return false
	}
	if (manifest.ControlDevice == 0) != (manifest.ControlInode == 0) || (manifest.SocketDevice == 0) != (manifest.SocketInode == 0) {
		return false
	}
	wantDir := filepath.Join(root, manifest.Generation)
	if filepath.Dir(path) != wantDir || manifest.SocketPath != filepath.Join(socketDir, strconv.Itoa(manifest.PID)+".sock") {
		return false
	}
	if manifest.Agenthail == "peer-relay" {
		return manifest.State == "ready" && manifest.ControlPath == "" && manifest.ControlDevice == 0 && manifest.ControlInode == 0 && manifest.RecordPath == "" && manifest.SocketInode != 0
	}
	if manifest.State == "ready" && (manifest.ControlInode == 0 || manifest.SocketInode == 0) {
		return false
	}
	if manifest.ControlPath != filepath.Join(wantDir, strconv.Itoa(manifest.PID)+".sock") {
		return false
	}
	home := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	return manifest.RecordPath == filepath.Join(surface.ClaudeConfigDir(home), "sessions", strconv.Itoa(manifest.PID)+".json")
}

// On Linux Claude Code records procStart as the starttime field of
// /proc/<pid>/stat, in clock ticks since boot.
func IsClockTicks(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// nativeProcessMatches reports whether pid is the process a native Claude
// record names, by the start time Claude recorded for it.
func nativeProcessMatches(pid int, recorded string) bool {
	if IsClockTicks(recorded) {
		actual, err := procstart.Ticks(pid)
		return err == nil && actual == recorded
	}
	return processMatches(pid, recorded)
}

func processMatches(pid int, expected string) bool {
	actual, err := procstart.Lstart(context.Background(), pid)
	return err == nil && actual == expected
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func processOwnsManifest(manifest OwnershipManifest) bool {
	if manifest.ProcessToken == "" || !processMatches(manifest.PID, manifest.ProcStart) {
		return false
	}
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(manifest.PID), "-o", "command=").Output()
	if err != nil {
		return false
	}
	wantToken := manifest.ProcessToken
	foundWorker := false
	foundToken := false
	for _, field := range strings.Fields(string(out)) {
		foundWorker = foundWorker || field == "claude-peer-worker" || field == "claude-peer-relay"
		foundToken = foundToken || field == wantToken
	}
	return foundWorker && foundToken
}

func processIsPeerWorker(pid int) bool {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && (strings.Contains(string(out), "agenthail claude-peer-worker") || strings.Contains(string(out), "agenthail claude-peer-relay"))
}
