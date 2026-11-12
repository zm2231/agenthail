package simhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const describeJSON = `[{"type":"Application","AXLabel":"Agenthail","frame":{"x":0,"y":0,"width":402,"height":874},"children":[
 {"type":"Group","AXLabel":null,"frame":{"x":0,"y":0,"width":402,"height":874},"children":[
  {"type":"Button","AXLabel":"Send","AXUniqueId":"composer.send","enabled":true,"frame":{"x":300,"y":800,"width":40,"height":20}},
  {"type":"TextField","AXLabel":"Message","AXValue":"hi","enabled":false,"frame":{"x":10,"y":800,"width":280,"height":20}}]}]}]`

func leased(t *testing.T) (*Host, *fakeSim) {
	t.Helper()
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	sim.describe = describeJSON
	h, _ := newTestHost(t, sim)
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	return h, sim
}

func TestDescribeGivesRefsThatTapTheirCentre(t *testing.T) {
	h, sim := leased(t)
	out := mustCall(t, h, "s1", Request{Action: "describe"})
	tree := out["tree"].(string)
	want := "- [ref=e1] Application \"Agenthail\" @(0,0 402x874)\n  - [ref=e2] Button \"Send\" id=\"composer.send\" @(300,800 40x20)\n  - [ref=e3] TextField \"Message\" value=\"hi\" disabled @(10,800 280x20)\n"
	if tree != want || out["truncated"] != false || out["elements"] != 3 {
		t.Fatalf("tree =\n%s\nwant\n%s(%v)", tree, want, out)
	}
	if data, err := os.ReadFile(out["file"].(string)); err != nil || string(data) != want {
		t.Fatalf("file = %q, %v", data, err)
	}
	mustCall(t, h, "s1", Request{Action: "tap", Ref: "e2"})
	if taps := sim.ran("axe tap -x 320 -y 810 --udid U1"); len(taps) != 1 {
		t.Fatalf("tap e2 ran %+v", sim.ran("axe tap"))
	}
	if code := refusalCode(t, h, "s1", Request{Action: "tap", Ref: "e9"}); code != "not_found" {
		t.Fatalf("unknown ref = %s", code)
	}
	cut := mustCall(t, h, "s1", Request{Action: "describe", Limit: 50})
	if cut["truncated"] != true || len(cut["tree"].(string)) > 50 || cut["full"] == nil {
		t.Fatalf("limited describe = %+v", cut)
	}
}

func TestTypeSendsTextOnStdin(t *testing.T) {
	h, sim := leased(t)
	mustCall(t, h, "s1", Request{Action: "type", Text: "--help me"})
	typed := sim.ran("axe type --stdin --udid U1")
	if len(typed) != 1 || string(typed[0].Stdin) != "--help me" {
		t.Fatalf("type ran %+v", sim.ran("axe type"))
	}
}

func TestInstallTakesAppsOnlyFromTheOwnersOutDir(t *testing.T) {
	h, sim := leased(t)
	outside := filepath.Join(t.TempDir(), "Elsewhere.app")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, h, "s1", Request{Action: "install", App: outside}); code != "out_of_scope" {
		t.Fatalf("install from outside = %s", code)
	}
	dir := h.ownerDir("s1")
	link := filepath.Join(dir, "Link.app")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, h, "s1", Request{Action: "install", App: link}); code != "out_of_scope" {
		t.Fatalf("install through a link out of outDir = %s", code)
	}
	other := filepath.Join(h.ownerDir("s2"), "Theirs.app")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, h, "s1", Request{Action: "install", App: other}); code != "out_of_scope" {
		t.Fatalf("install from another owner's outDir = %s", code)
	}
	mine := filepath.Join(dir, "Mine.app")
	if err := os.MkdirAll(mine, 0o700); err != nil {
		t.Fatal(err)
	}
	mustCall(t, h, "s1", Request{Action: "install", App: mine})
	installs := sim.ran("xcrun simctl install U1")
	if len(installs) != 1 || !strings.HasSuffix(installs[0].Args[3], "Mine.app") {
		t.Fatalf("installs = %+v", installs)
	}
}

func TestLaunchPassesEnvThroughSimctl(t *testing.T) {
	h, sim := leased(t)
	out := mustCall(t, h, "s1", Request{Action: "launch", BundleID: "com.example.app", Args: []string{"-UITest"}, Env: map[string]string{"B": "2", "A": "1"}})
	if out["pid"] != 4242 {
		t.Fatalf("launch = %+v", out)
	}
	launch := sim.ran("xcrun simctl launch --terminate-running-process U1 com.example.app -UITest")
	if len(launch) != 1 || strings.Join(launch[0].Env, " ") != "SIMCTL_CHILD_A=1 SIMCTL_CHILD_B=2" {
		t.Fatalf("launch ran %+v", sim.ran("xcrun simctl launch"))
	}
	if code := refusalCode(t, h, "s1", Request{Action: "launch", BundleID: "com.example.app", Env: map[string]string{"A=B": "1"}}); code != "invalid" {
		t.Fatalf("bad env name = %s", code)
	}
}

func TestPushLocationAndLogs(t *testing.T) {
	h, sim := leased(t)
	if code := refusalCode(t, h, "s1", Request{Action: "push", BundleID: "com.example.app", Payload: []byte(`{"title":"x"}`)}); code != "invalid" {
		t.Fatalf("push without aps = %s", code)
	}
	mustCall(t, h, "s1", Request{Action: "push", BundleID: "com.example.app", Payload: []byte(`{"aps":{"alert":"hi"}}`)})
	if len(sim.ran("xcrun simctl push U1 com.example.app")) != 1 {
		t.Fatal("push not sent")
	}
	mustCall(t, h, "s1", Request{Action: "location", Latitude: ptr(51.5), Longitude: ptr(-0.12)})
	if len(sim.ran("xcrun simctl location U1 set 51.5,-0.12")) != 1 {
		t.Fatalf("location ran %+v", sim.ran("xcrun simctl location"))
	}
	logs := mustCall(t, h, "s1", Request{Action: "logs", BundleID: "com.example.app", Seconds: 30, Limit: 22})
	if logs["log"] != "line two\nline three" || logs["truncated"] != true {
		t.Fatalf("logs = %+v", logs)
	}
	if len(sim.ran(`xcrun simctl spawn U1 log show --last 30s --style compact --predicate process == "Agenthail"`)) != 1 {
		t.Fatalf("logs ran %+v", sim.ran("xcrun simctl spawn"))
	}
}
