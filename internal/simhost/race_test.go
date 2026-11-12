package simhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestTwoHostsAndAScriptRunCreateOneDevice(t *testing.T) {
	sim := newFakeSim()
	lockDir := t.TempDir()
	newHost := func() *Host {
		h := New(Config{Exec: sim.exec, OutRoot: t.TempDir(), LockDir: lockDir, GOOS: "darwin"})
		t.Cleanup(func() { h.Release("a", ""); h.Release("b", "") })
		return h
	}
	script, err := os.OpenFile(filepath.Join(lockDir, "agenthail-simulator-create.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(script.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	first, second := newHost(), newHost()
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, h := range []*Host{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = h.Call(context.Background(), []string{"a", "b"}[i], Request{Action: "acquire", Kind: "iphone"})
		}()
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(sim.ran("xcrun simctl create")); n != 0 {
		t.Fatalf("created %d devices while the script held the create lock", n)
	}
	script.Close()
	wg.Wait()
	if n := len(sim.ran("xcrun simctl create")); n != 1 {
		t.Fatalf("created %d devices, want one", n)
	}
	busy := 0
	for _, err := range results {
		if err != nil {
			if refusal, ok := err.(*Error); !ok || refusal.Code != "busy" {
				t.Fatalf("acquire = %v", err)
			}
			busy++
		}
	}
	if busy != 1 {
		t.Fatalf("results %v, want one lease and one busy", results)
	}
}

func TestArchiveCleanupLeavesTheOwnersNewLeaseAndFiles(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	sim.devices["U2"] = "Agenthail iPad"
	sim.describe = describeJSON
	h, _ := newTestHost(t, sim)
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	mustCall(t, h, "s1", Request{Action: "launch", BundleID: "com.example.app"})
	gate := make(chan struct{})
	sim.mu.Lock()
	sim.block["xcrun simctl terminate U1"] = gate
	sim.mu.Unlock()
	detached := h.Detach("s1", "")
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.End(detached)
		h.RemoveIdleOwner("s1")
	}()
	if code := refusalCode(t, h, "s1", Request{Action: "acquire", Kind: "iphone"}); code != "busy" {
		t.Fatalf("reacquire while the old lease is cleaning up = %s", code)
	}
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "ipad"})
	file := mustCall(t, h, "s1", Request{Action: "describe"})["file"].(string)
	close(gate)
	<-done
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("cleanup removed the new lease's output: %v", err)
	}
	if !h.Holds("s1") {
		t.Fatal("cleanup ended the new lease")
	}
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
}

func TestReleaseWaitsForALaunchInFlightAndTerminatesIt(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	h, _ := newTestHost(t, sim)
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	gate := make(chan struct{})
	started := make(chan struct{})
	sim.mu.Lock()
	sim.block["xcrun simctl launch"] = gate
	sim.onCall = func(cmd Command) {
		if len(cmd.Args) > 1 && cmd.Args[1] == "launch" {
			close(started)
		}
	}
	sim.mu.Unlock()
	launched := make(chan error, 1)
	go func() {
		_, err := h.Call(context.Background(), "s1", Request{Action: "launch", BundleID: "com.example.late"})
		launched <- err
	}()
	<-started
	released := make(chan []string, 1)
	go func() { released <- h.Release("s1", "") }()
	select {
	case <-released:
		t.Fatal("release finished while a launch was still running")
	case <-time.After(200 * time.Millisecond):
	}
	close(gate)
	if err := <-launched; err != nil {
		t.Fatal(err)
	}
	<-released
	if len(sim.ran("xcrun simctl terminate U1 com.example.late")) != 1 {
		t.Fatal("the app launched while release waited was not terminated")
	}
	if code := refusalCode(t, h, "s1", Request{Action: "button", Button: "home"}); code != "not_held" {
		t.Fatalf("an action after release = %s", code)
	}
}

func TestInstallCopiesOnlyWhatIsInsideTheOwnersFolder(t *testing.T) {
	h, sim := leased(t)
	dir := h.ownerDir("s1")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret"), "x")
	app := filepath.Join(dir, "Linked.app")
	if err := os.MkdirAll(app, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(app, "secret")); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, h, "s1", Request{Action: "install", App: app}); code != "invalid" {
		t.Fatalf("install of an app holding a link = %s", code)
	}
	if err := os.MkdirAll(filepath.Join(outside, "Far.app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "away")); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, h, "s1", Request{Action: "install", App: filepath.Join(dir, "away", "Far.app")}); code != "out_of_scope" {
		t.Fatalf("install through a linked folder = %s", code)
	}
	good := filepath.Join(dir, "Good.app")
	if err := os.MkdirAll(filepath.Join(good, "Frameworks"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(good, "Info.plist"), "plist")
	writeFile(t, filepath.Join(good, "Frameworks", "lib"), "lib")
	var staged string
	sim.mu.Lock()
	sim.onCall = func(cmd Command) {
		if len(cmd.Args) > 3 && cmd.Args[1] == "install" {
			staged = cmd.Args[3]
			if data, err := os.ReadFile(filepath.Join(staged, "Frameworks", "lib")); err != nil || string(data) != "lib" {
				t.Errorf("the staged copy is incomplete: %q, %v", data, err)
			}
		}
	}
	sim.mu.Unlock()
	mustCall(t, h, "s1", Request{Action: "install", App: good})
	if !strings.HasPrefix(staged, filepath.Join(h.outRoot, ".staging")) || strings.HasPrefix(staged, dir) {
		t.Fatalf("simctl installed %q, want a copy outside the owner's folder", staged)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("the staged copy was left behind: %v", err)
	}
}
