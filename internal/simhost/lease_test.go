package simhost

import (
	"bytes"
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStatusNamesWhatIsMissing(t *testing.T) {
	sim := newFakeSim()
	sim.missing["axe"] = true
	h, _ := newTestHost(t, sim)
	status := h.Status(context.Background())
	if status.Ready || !strings.Contains(status.Problem, "AXe") || !strings.Contains(status.Fix, "brew install") {
		t.Fatalf("status without AXe = %+v", status)
	}
	linux := New(Config{Exec: newFakeSim().exec, GOOS: "linux"})
	if status := linux.Status(context.Background()); status.Ready || !strings.Contains(status.Problem, "macOS") {
		t.Fatalf("status on linux = %+v", status)
	}
	ready := newFakeSim()
	h, _ = newTestHost(t, ready)
	if status := h.Status(context.Background()); !status.Ready || status.AXe != "1.8.0" || !status.Simslim {
		t.Fatalf("ready status = %+v", status)
	}
}

func TestAcquireCreatesTheSharedDeviceSlimmedAndNamedLast(t *testing.T) {
	sim := newFakeSim()
	h, _ := newTestHost(t, sim)
	out := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "ipad"})
	lease := out["lease"].(LeaseView)
	if lease.UDID != "UDID-1" || lease.Device != "Agenthail iPad" {
		t.Fatalf("lease = %+v", lease)
	}
	create := sim.ran("xcrun simctl create")
	if len(create) != 1 || create[0].Args[2] != "Agenthail iPad (slimming)" || create[0].Args[3] != "t.iPad-Pro-11" || create[0].Args[4] != "com.apple.CoreSimulator.SimRuntime.iOS-27-0" {
		t.Fatalf("create = %+v", create)
	}
	if len(sim.ran("simslim on UDID-1 --profile")) != 1 || sim.devices["UDID-1"] != "Agenthail iPad" {
		t.Fatalf("not slimmed then named: %+v", sim.devices)
	}
	mustCall(t, h, "s1", Request{Action: "release", Kind: "ipad"})
	mustCall(t, h, "s2", Request{Action: "acquire", Kind: "ipad"})
	if sim.created != 1 {
		t.Fatalf("created %d devices, want the one shared device", sim.created)
	}
}

func TestEmbeddedProfileIsTheScriptsProfile(t *testing.T) {
	script, err := os.ReadFile("../../scripts/simslim-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(script, simslimProfile) {
		t.Fatal("internal/simhost/simslim-profile.json differs from scripts/simslim-profile.json")
	}
}

func TestOneHolderPerDevice(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	h, _ := newTestHost(t, sim)
	first := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})["lease"].(LeaseView)
	again := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})["lease"].(LeaseView)
	if again.UDID != first.UDID || !again.Acquired.Equal(first.Acquired) {
		t.Fatalf("acquire again = %+v, want the same lease %+v", again, first)
	}
	if code := refusalCode(t, h, "s2", Request{Action: "acquire", Kind: "iphone"}); code != "busy" {
		t.Fatalf("second session acquire = %s", code)
	}
	if code := refusalCode(t, h, "s2", Request{Action: "tap", X: ptr(1), Y: ptr(1)}); code != "not_held" {
		t.Fatalf("tap without a lease = %s", code)
	}
	list := mustCall(t, h, "s2", Request{Action: "list"})["simulators"].([]map[string]any)
	if list[1]["kind"] != "iphone" || list[1]["holder"] != "another session" || list[1]["lease"] != nil {
		t.Fatalf("list for another session = %+v", list)
	}
}

func TestALeaseTakesTheLockScriptRunsTake(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	h, _ := newTestHost(t, sim)
	script, err := os.OpenFile(h.lockPath("U1"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(script.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, h, "s1", Request{Action: "acquire", Kind: "iphone"}); code != "busy" {
		t.Fatalf("acquire while a script run holds the lock = %s", code)
	}
	script.Close()
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	probe, err := os.OpenFile(h.lockPath("U1"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("a script run could take the lock while a lease holds it")
	}
	mustCall(t, h, "s1", Request{Action: "release"})
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("release kept the lock: %v", err)
	}
}

func TestLeasesEndIdleOrAtTheirLimitAndCleanUp(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	h, c := newTestHost(t, sim)
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	mustCall(t, h, "s1", Request{Action: "launch", BundleID: "com.example.app"})
	c.now = c.now.Add(IdleLimit - time.Second)
	mustCall(t, h, "s1", Request{Action: "button", Button: "home"})
	c.now = c.now.Add(IdleLimit - time.Second)
	if ended := h.Sweep(); len(ended) != 0 {
		t.Fatalf("a used lease ended: %v", ended)
	}
	c.now = c.now.Add(2 * time.Second)
	if ended := h.Sweep(); len(ended) != 1 || ended[0] != "iphone" {
		t.Fatalf("idle lease not ended: %v", ended)
	}
	if len(sim.ran("xcrun simctl terminate U1 com.example.app")) != 1 || len(sim.ran("xcrun simctl location U1 clear")) != 1 || len(sim.ran("xcrun simctl status_bar U1 clear")) != 1 {
		t.Fatal("ending the lease did not terminate its app and reset location and status bar")
	}
	if len(sim.ran("xcrun simctl erase")) != 0 || len(sim.ran("xcrun simctl ui")) != 0 {
		t.Fatal("ending a lease must not erase the device or change its appearance")
	}

	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	for step := 0; step < 6; step++ {
		c.now = c.now.Add(9 * time.Minute)
		mustCall(t, h, "s1", Request{Action: "button", Button: "home"})
	}
	c.now = c.now.Add(7 * time.Minute)
	if code := refusalCode(t, h, "s1", Request{Action: "button", Button: "home"}); code != "not_held" {
		t.Fatalf("a lease used every 9 minutes outlived its 60 minute limit: %s", code)
	}

	start := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})["lease"].(LeaseView)
	for step := 0; step < 6; step++ {
		c.now = c.now.Add(9 * time.Minute)
		mustCall(t, h, "s1", Request{Action: "button", Button: "home"})
	}
	renewed := mustCall(t, h, "s1", Request{Action: "renew"})["lease"].(LeaseView)
	if !renewed.EndsAt.Equal(c.now.Add(LeaseLimit)) || !renewed.EndsAt.After(start.EndsAt) {
		t.Fatalf("renew = %+v (was %+v)", renewed, start)
	}
}

func TestNoVoiceButtons(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	h, _ := newTestHost(t, sim)
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	for _, button := range []string{"siri", "apple-pay"} {
		if code := refusalCode(t, h, "s1", Request{Action: "button", Button: button}); code != "invalid" {
			t.Fatalf("button %s = %s", button, code)
		}
	}
	if len(sim.ran("axe button")) != 0 {
		t.Fatal("a refused button reached AXe")
	}
}

func ptr(v float64) *float64 { return &v }
