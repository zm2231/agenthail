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

const describeScalarsJSON = `[{"AXFrame":"{{0, 0}, {402, 874}}","AXUniqueId":null,"frame":{"y":0,"x":0,"width":402,"height":874},"role_description":"application","AXLabel":"Rune","content_required":false,"type":"Application","title":null,"help":null,"custom_actions":[],"AXValue":null,"enabled":true,"role":"AXApplication","subrole":null,"pid":4242,"children":[
 {"AXFrame":"{{16, 120}, {370, 44}}","AXUniqueId":null,"frame":{"y":120,"x":16,"width":370,"height":44},"role_description":"text","AXLabel":"Name","content_required":false,"type":"StaticText","title":null,"help":null,"custom_actions":[],"AXValue":"Ada","enabled":true,"role":"AXStaticText","subrole":null,"children":[]},
 {"AXFrame":"{{16, 164}, {370, 44}}","AXUniqueId":null,"frame":{"y":164,"x":16,"width":370,"height":44},"role_description":"text","AXLabel":"Cards","content_required":false,"type":"StaticText","title":null,"help":null,"custom_actions":[],"AXValue":12,"enabled":true,"role":"AXStaticText","subrole":null,"children":[]},
 {"AXFrame":"{{16, 208}, {370, 44}}","AXUniqueId":"volume","frame":{"y":208,"x":16,"width":370,"height":44},"role_description":"slider","AXLabel":"Volume","content_required":false,"type":"Slider","title":null,"help":null,"custom_actions":[],"AXValue":0.75,"enabled":true,"role":"AXSlider","subrole":null,"children":[]},
 {"AXFrame":"{{16, 252}, {370, 44}}","AXUniqueId":null,"frame":{"y":252,"x":16,"width":370,"height":44},"role_description":"switch","AXLabel":"Sync","content_required":false,"type":"Switch","title":null,"help":null,"custom_actions":[],"AXValue":true,"enabled":0,"role":"AXCheckBox","subrole":"AXSwitch","children":[]},
 {"AXFrame":"{{0, 790}, {100, 49}}","AXUniqueId":null,"frame":{"y":790,"x":0,"width":100,"height":49},"role_description":"button","AXLabel":"Inbox","content_required":false,"type":"Button","title":null,"help":null,"custom_actions":[],"AXValue":false,"enabled":true,"role":"AXButton","subrole":null,"children":[]}]}]`

func TestDescribeRendersEveryScalarValueAsText(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	sim.describe = describeScalarsJSON
	h, _ := newTestHost(t, sim)
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	out := mustCall(t, h, "s1", Request{Action: "describe"})
	want := "- [ref=e1] Application \"Rune\" @(0,0 402x874)\n" +
		"  - [ref=e2] StaticText \"Name\" value=\"Ada\" @(16,120 370x44)\n" +
		"  - [ref=e3] StaticText \"Cards\" value=\"12\" @(16,164 370x44)\n" +
		"  - [ref=e4] Slider \"Volume\" id=\"volume\" value=\"0.75\" @(16,208 370x44)\n" +
		"  - [ref=e5] Switch \"Sync\" value=\"true\" disabled @(16,252 370x44)\n" +
		"  - [ref=e6] Button \"Inbox\" value=\"false\" @(0,790 100x49)\n"
	if tree := out["tree"].(string); tree != want {
		t.Fatalf("tree =\n%s\nwant\n%s", tree, want)
	}
}

const describeActionsJSON = `[{"AXFrame":"{{0, 0}, {402, 874}}","AXUniqueId":null,"frame":{"y":0,"x":0,"width":402,"height":874},"role_description":"application","AXLabel":"Home screen","content_required":false,"type":"Application","title":null,"help":null,"custom_actions":[],"AXValue":null,"enabled":true,"role":"AXApplication","subrole":null,"pid":88,"traits":[],"children":[
 {"AXFrame":"{{0, 60}, {402, 700}}","AXUniqueId":null,"frame":{"y":60,"x":0,"width":402,"height":700},"role_description":"group","AXLabel":null,"content_required":false,"type":"Group","title":null,"help":null,"custom_actions":null,"AXValue":null,"enabled":true,"role":"AXGroup","subrole":null,"traits":["Header"],"children":[
  {"AXFrame":"{{27, 80}, {64, 64}}","AXUniqueId":"Messages","frame":{"y":80,"x":27,"width":64,"height":64},"role_description":"button","AXLabel":"Messages","content_required":false,"type":"Button","title":null,"help":"Double tap to open","custom_actions":["Edit mode","Today","App Library"],"AXValue":null,"enabled":true,"role":"AXButton","subrole":null,"traits":["Button","Selected"],"children":[]},
  {"AXFrame":"{{27, 180}, {64, 64}}","AXUniqueId":null,"frame":{"y":180,"x":27,"width":64,"height":64},"role_description":"button","AXLabel":"","content_required":false,"type":"Button","title":null,"help":null,"custom_actions":["Remove App"," ",""],"AXValue":null,"enabled":true,"role":"AXButton","subrole":null,"traits":["Button"],"children":[]}]}]}]`

func TestDescribeShowsTraitsAndVoiceOverActions(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	sim.describe = describeActionsJSON
	h, _ := newTestHost(t, sim)
	mustCall(t, h, "s1", Request{Action: "acquire", Kind: "iphone"})
	out := mustCall(t, h, "s1", Request{Action: "describe"})
	want := "- [ref=e1] Application \"Home screen\" @(0,0 402x874)\n" +
		"  - [ref=e2] Button \"Messages\" id=\"Messages\" traits=Button,Selected actions=[\"Edit mode\",\"Today\",\"App Library\"] @(27,80 64x64)\n" +
		"  - [ref=e3] Button traits=Button actions=[\"Remove App\"] @(27,180 64x64)\n"
	if tree := out["tree"].(string); tree != want {
		t.Fatalf("tree =\n%s\nwant\n%s", tree, want)
	}
}

func TestUninstallRemovesAnAppOnlyFromTheCallersLeasedDevice(t *testing.T) {
	sim := newFakeSim()
	sim.devices["U1"] = "Agenthail iPhone"
	h, _ := newTestHost(t, sim)
	if code := refusalCode(t, h, "s1", Request{Action: "uninstall", BundleID: "com.example.app"}); code != "not_held" {
		t.Fatalf("uninstall without a lease = %s", code)
	}
	mustCall(t, h, "s2", Request{Action: "acquire", Kind: "iphone"})
	if code := refusalCode(t, h, "s1", Request{Action: "uninstall", BundleID: "com.example.app"}); code != "not_held" {
		t.Fatalf("uninstall while another session holds the device = %s", code)
	}
	if len(sim.ran("xcrun simctl uninstall")) != 0 {
		t.Fatal("a refused uninstall reached simctl")
	}
	if code := refusalCode(t, h, "s2", Request{Action: "uninstall"}); code != "invalid" {
		t.Fatalf("uninstall without a bundleId = %s", code)
	}
	out := mustCall(t, h, "s2", Request{Action: "uninstall", BundleID: "com.example.app"})
	if out["uninstalled"] != "com.example.app" || len(sim.ran("xcrun simctl uninstall U1 com.example.app")) != 1 {
		t.Fatalf("uninstall = %v, ran %+v", out, sim.ran("xcrun simctl uninstall"))
	}
}

func TestKeyPressesHardwareKeysTheTypeActionCannot(t *testing.T) {
	h, sim := leased(t)
	tab := 43
	cases := []struct {
		req  Request
		want string
	}{
		{Request{Action: "key", Key: &tab}, "axe key 43 --udid U1"},
		{Request{Action: "key", Key: &tab, Modifiers: []int{225}}, "axe key-combo --modifiers 225 --key 43 --udid U1"},
		{Request{Action: "key", Keys: []int{43, 43, 40}}, "axe key-sequence --keycodes 43,43,40 --udid U1"},
	}
	for _, c := range cases {
		mustCall(t, h, "s1", c.req)
		if len(sim.ran(c.want)) != 1 {
			t.Fatalf("%+v ran %+v, want %q", c.req, sim.ran("axe key"), c.want)
		}
	}
	big, zero, shift := 256, 0, 225
	refused := []Request{
		{Action: "key"},
		{Action: "key", Key: &tab, Keys: []int{43}},
		{Action: "key", Key: &big},
		{Action: "key", Keys: []int{4, -1}},
		{Action: "key", Keys: make([]int, 101)},
		{Action: "key", Keys: []int{43}, Modifiers: []int{225}},
		{Action: "key", Key: &tab, Modifiers: []int{43}},
		{Action: "key", Key: &tab, Modifiers: []int{225, 225}},
		{Action: "key", Key: &shift, Modifiers: []int{225}},
	}
	before := len(sim.ran("axe key"))
	for _, req := range refused {
		if code := refusalCode(t, h, "s1", req); code != "invalid" {
			t.Fatalf("%+v = %s, want invalid", req, code)
		}
	}
	if len(sim.ran("axe key")) != before {
		t.Fatalf("a refused key reached axe: %+v", sim.ran("axe key"))
	}
	if code := refusalCode(t, h, "s2", Request{Action: "key", Key: &zero}); code != "not_held" {
		t.Fatalf("key without a lease = %s", code)
	}
}
