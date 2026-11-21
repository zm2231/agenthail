package simhost

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	ios18 = "com.apple.CoreSimulator.SimRuntime.iOS-18-5"
	ios27 = "com.apple.CoreSimulator.SimRuntime.iOS-27-0"
)

func TestAcquireWithARuntimeCreatesItsOwnDeviceOnFirstRequestOnly(t *testing.T) {
	sim := newFakeSim()
	sim.devices["SHARED"] = "Agenthail iPhone"
	h, _ := newTestHost(t, sim)
	lease := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone", Runtime: "18.5"})["lease"].(LeaseView)
	if lease.Device != "Agenthail iPhone (iOS 18.5)" || lease.Runtime != "iOS 18.5" || lease.UDID != "UDID-1" {
		t.Fatalf("lease = %+v", lease)
	}
	create := sim.ran("xcrun simctl create")
	if len(create) != 1 || create[0].Args[2] != "Agenthail iPhone (iOS 18.5) (slimming)" || create[0].Args[3] != "t.iPhone-16" || create[0].Args[4] != ios18 {
		t.Fatalf("create = %+v", create)
	}
	if len(sim.ran("simslim on UDID-1 --profile")) != 1 || sim.devices["UDID-1"] != "Agenthail iPhone (iOS 18.5)" {
		t.Fatalf("not slimmed then named: %+v", sim.devices)
	}
	mustCall(t, h, "s1", Request{Action: "release", Kind: "iphone", Runtime: "18.5"})
	for _, want := range []string{"18.5", "iOS 18.5", "18", ios18} {
		lease := mustCall(t, h, "s2", Request{Action: "acquire", Kind: "iphone", Runtime: want})["lease"].(LeaseView)
		if lease.UDID != "UDID-1" {
			t.Fatalf("runtime %q leased %+v", want, lease)
		}
		mustCall(t, h, "s2", Request{Action: "release", Kind: "iphone", Runtime: want})
	}
	if sim.created != 1 {
		t.Fatalf("created %d devices, want one for iOS 18.5", sim.created)
	}
}

func TestARuntimeTheSharedDeviceIsOnLeasesTheSharedDevice(t *testing.T) {
	sim := newFakeSim()
	sim.devices["SHARED"] = "Agenthail iPhone"
	h, _ := newTestHost(t, sim)
	lease := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone", Runtime: "27.0"})["lease"].(LeaseView)
	if lease.UDID != "SHARED" || lease.Device != "Agenthail iPhone" || sim.created != 0 {
		t.Fatalf("lease = %+v, created %d", lease, sim.created)
	}
	if code := refusalCode(t, h, "s2", Request{Action: "acquire", Kind: "iphone"}); code != "busy" {
		t.Fatalf("the shared device leased by runtime was free to the default path: %s", code)
	}

	fresh := newFakeSim()
	h, _ = newTestHost(t, fresh)
	lease = mustCall(t, h, "s1", Request{Action: "acquire", Kind: "ipad", Runtime: "27"})["lease"].(LeaseView)
	if lease.Device != "Agenthail iPad" || fresh.devices[lease.UDID] != "Agenthail iPad" {
		t.Fatalf("the newest runtime with no shared device yet made %+v, want the shared device", lease)
	}
}

func TestAMissingRuntimeFailsWithTheInstalledOnesAndDownloadsNothing(t *testing.T) {
	sim := newFakeSim()
	h, _ := newTestHost(t, sim)
	for _, want := range []string{"17.4", "19", "visionOS 2.0"} {
		_, err := h.Call(context.Background(), "s1", Request{Action: "acquire", Kind: "iphone", Runtime: want})
		var refusal *Error
		if !errors.As(err, &refusal) || refusal.Code != "invalid" || !strings.Contains(refusal.Message, "18.5, 27.0") {
			t.Fatalf("runtime %q: %v", want, err)
		}
		if installed, _ := refusal.Detail["installed"].([]string); strings.Join(installed, ",") != "18.5,27.0" {
			t.Fatalf("runtime %q detail = %+v", want, refusal.Detail)
		}
	}
	for _, cmd := range sim.calls {
		line := strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
		if strings.Contains(line, "download") || strings.Contains(line, "simctl create") || strings.Contains(line, "runtime add") {
			t.Fatalf("a missing runtime ran %q", line)
		}
	}
}

func TestARuntimeDeviceNameOnAnotherRuntimeIsAConflict(t *testing.T) {
	sim := newFakeSim()
	sim.devices["SHARED"] = "Agenthail iPhone"
	sim.devices["STRAY"] = "Agenthail iPhone (iOS 18.5)"
	sim.runtimes["STRAY"] = ios27
	h, _ := newTestHost(t, sim)
	if code := refusalCode(t, h, "s1", Request{Action: "acquire", Kind: "iphone", Runtime: "18.5"}); code != "conflict" {
		t.Fatalf("a same-named device on iOS 27 was not a conflict: %s", code)
	}
	if sim.created != 0 {
		t.Fatalf("created %d devices beside the stray one", sim.created)
	}
	lease := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})["lease"].(LeaseView)
	if lease.UDID != "SHARED" {
		t.Fatalf("the default path took %+v, want the shared device", lease)
	}
}

func TestTheDefaultPathIsUnchangedBesideRuntimeDevices(t *testing.T) {
	sim := newFakeSim()
	sim.devices["SHARED"] = "Agenthail iPhone"
	sim.runtimes["SHARED"] = ios18
	sim.devices["OLD"] = "Agenthail iPhone (iOS 18.5)"
	sim.runtimes["OLD"] = ios18
	h, _ := newTestHost(t, sim)
	lease := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})["lease"].(LeaseView)
	if lease.UDID != "SHARED" || lease.Device != "Agenthail iPhone" || sim.created != 0 {
		t.Fatalf("default acquire = %+v, created %d", lease, sim.created)
	}
	if len(sim.ran("xcrun simctl list runtimes")) != 0 {
		t.Fatal("the default path read runtimes for a device that exists")
	}
	if code := refusalCode(t, h, "s2", Request{Action: "acquire", Kind: "iphone", Runtime: "18.5"}); code != "busy" {
		t.Fatalf("iOS 18.5 while the shared device is on it and leased = %s, want busy", code)
	}
}

func TestOneSessionHoldsTwoRuntimesAndNamesWhichOne(t *testing.T) {
	sim := newFakeSim()
	h, _ := newTestHost(t, sim)
	shared := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})["lease"].(LeaseView)
	old := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone", Runtime: "18.5"})["lease"].(LeaseView)
	if shared.UDID == old.UDID || shared.Runtime != "iOS 27.0" || old.Runtime != "iOS 18.5" {
		t.Fatalf("shared %+v, versioned %+v", shared, old)
	}
	if code := refusalCode(t, h, "s1", Request{Action: "button", Kind: "iphone", Button: "home"}); code != "invalid" {
		t.Fatalf("an unnamed call between two held devices = %s", code)
	}
	mustCall(t, h, "s1", Request{Action: "button", Kind: "iphone", Runtime: "18.5", Button: "home"})
	if pressed := sim.ran("axe button home --udid " + old.UDID); len(pressed) != 1 {
		t.Fatalf("the press did not reach the iOS 18.5 device: %+v", sim.calls)
	}
	released := mustCall(t, h, "s1", Request{Action: "release", Runtime: "18.5"})["released"].([]string)
	if strings.Join(released, ",") != "iphone (iOS 18.5)" || !h.Holds("s1") {
		t.Fatalf("released %v", released)
	}
	if got := h.Release("s1", ""); strings.Join(got, ",") != "iphone" {
		t.Fatalf("archive release = %v", got)
	}
}

func TestARuntimeLeaseCleansUpLikeTheSharedOne(t *testing.T) {
	sim := newFakeSim()
	h, c := newTestHost(t, sim)
	lease := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "ipad", Runtime: "18.5"})["lease"].(LeaseView)
	mustCall(t, h, "s1", Request{Action: "launch", Runtime: "18.5", BundleID: "com.example.app"})
	c.now = c.now.Add(IdleLimit)
	if ended := h.Sweep(); strings.Join(ended, ",") != "ipad (iOS 18.5)" {
		t.Fatalf("sweep ended %v", ended)
	}
	for _, want := range []string{"xcrun simctl terminate " + lease.UDID + " com.example.app", "xcrun simctl location " + lease.UDID + " clear", "xcrun simctl status_bar " + lease.UDID + " clear"} {
		if len(sim.ran(want)) != 1 {
			t.Fatalf("cleanup did not run %q", want)
		}
	}
	if len(sim.ran("xcrun simctl erase")) != 0 || len(sim.ran("xcrun simctl delete")) != 0 {
		t.Fatal("cleanup erased or deleted the device")
	}
}

func TestListAndStatusNameEveryAgenthailDeviceWithItsRuntime(t *testing.T) {
	sim := newFakeSim()
	sim.devices["SHARED"] = "Agenthail iPhone"
	sim.devices["OLD"] = "Agenthail iPhone (iOS 18.5)"
	sim.runtimes["OLD"] = ios18
	sim.devices["HALF"] = "Agenthail iPad (iOS 18.5) (slimming)"
	sim.devices["MINE"] = "My iPhone"
	h, _ := newTestHost(t, sim)
	out := mustCall(t, h, "s1", Request{Action: "list"})
	var got []string
	for _, entry := range out["simulators"].([]map[string]any) {
		got = append(got, entry["device"].(string)+"="+func() string { r, _ := entry["runtime"].(string); return r }())
	}
	if strings.Join(got, ";") != "Agenthail iPad=;Agenthail iPhone=iOS 27.0;Agenthail iPhone (iOS 18.5)=iOS 18.5" {
		t.Fatalf("list = %v", got)
	}
	if runtimes := out["runtimes"].([]string); strings.Join(runtimes, ",") != "18.5,27.0" {
		t.Fatalf("runtimes = %v", runtimes)
	}
	status := h.Status(context.Background())
	var devices []string
	for _, d := range status.Devices {
		devices = append(devices, d.Device+"="+d.Runtime)
	}
	if strings.Join(devices, ";") != "Agenthail iPhone=iOS 27.0;Agenthail iPhone (iOS 18.5)=iOS 18.5" {
		t.Fatalf("status devices = %v", devices)
	}
}

func TestAShortVersionNamingTwoRuntimesIsRefusedEvenWithALeaseHeld(t *testing.T) {
	sim := newFakeSim()
	sim.runtimesJSON = `{"runtimes":[{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-18-5","isAvailable":true},{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-18-6","isAvailable":true},{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-27-0","isAvailable":true}]}`
	h, _ := newTestHost(t, sim)
	if code := refusalCode(t, h, "s1", Request{Action: "acquire", Kind: "iphone", Runtime: "18"}); code != "invalid" {
		t.Fatalf("18 with 18.5 and 18.6 installed = %s", code)
	}
	held := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone", Runtime: "18.5"})["lease"].(LeaseView)
	for _, owner := range []string{"s1", "s2"} {
		if code := refusalCode(t, h, owner, Request{Action: "acquire", Kind: "iphone", Runtime: "18"}); code != "invalid" {
			t.Fatalf("18 for %s while 18.5 is leased = %s", owner, code)
		}
	}
	if lease := mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone", Runtime: "18.5"})["lease"].(LeaseView); lease.UDID != held.UDID {
		t.Fatalf("the held lease changed: %+v", lease)
	}
}

func TestReleaseRefusesAKindThatNamesTwoHeldDevices(t *testing.T) {
	sim := newFakeSim()
	h, _ := newTestHost(t, sim)
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone", Runtime: "18.5"})
	if code := refusalCode(t, h, "s1", Request{Action: "release", Kind: "iphone"}); code != "invalid" {
		t.Fatalf("a kind-only release over two devices = %s", code)
	}
	if out := mustCall(t, h, "s1", Request{Action: "list"}); len(out["simulators"].([]map[string]any)) != 3 || !h.Holds("s1") {
		t.Fatal("a refused release ended a lease")
	}
	if released := mustCall(t, h, "s1", Request{Action: "release", Kind: "iphone", Runtime: "27.0"})["released"].([]string); strings.Join(released, ",") != "iphone" {
		t.Fatalf("released %v, want the shared iPhone", released)
	}
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "ipad"})
	if released := mustCall(t, h, "s1", Request{Action: "release"})["released"].([]string); strings.Join(released, ",") != "ipad,iphone (iOS 18.5)" {
		t.Fatalf("release without kind ended %v, want every lease", released)
	}
}
