package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/surface"
)

const fanoutGolden = "testdata/channel_fanout_example.txt"

// The daemon's answer for the fixture's crew: idle, busy, unacknowledged, read-only and paired-host members.
func crewReceipt() string {
	return `{"error":{"code":"conflict","message":"channel delivery: 1 sent, 2 queued, 1 submitted, 1 failed"},` +
		`"target":{"name":"crew","channel":"crew"},"sent":1,"queued":2,"submitted":1,"failed":1,"recipients":[` +
		`{"sessionId":"` + vpsBuilder.ID + `","hostId":"` + vpsHostID + `","outcome":"queued","detail":"held for its host"},` +
		`{"sessionId":"s-analyst","outcome":"submitted","detail":"Submitted to s-analyst."},` +
		`{"sessionId":"s-coder","outcome":"queued","detail":"Queued for s-coder; sends when current turn ends."},` +
		`{"sessionId":"s-reviewer","outcome":"failed","detail":"Codex session is read only: Codex Desktop created it, or Codex recorded no creator, and the Desktop bridge is unavailable; run 'agenthail launch codex'"},` +
		`{"sessionId":"s-scout","outcome":"sent","detail":"Sent to s-scout."}]}`
}

func fanoutFixture(t *testing.T) (*App, *cliSurface, *fakeDaemonAPI) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	app, _, fake, api := federatedCLIFixture(t, true)
	writable := func(id string, status surface.SessionStatus) surface.Session {
		return surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: status}
	}
	fake.sessions = map[string]surface.Session{
		"s-scout":    writable("s-scout", surface.StatusIdle),
		"s-coder":    writable("s-coder", surface.StatusBusy),
		"s-analyst":  writable("s-analyst", surface.StatusIdle),
		"s-reviewer": writable("s-reviewer", surface.StatusIdle),
	}
	for _, name := range []string{"scout", "coder", "analyst", "reviewer"} {
		if _, err := captureStdout(t, func() error { return app.Run([]string{"identify", "codex:s-" + name, name}) }); err != nil {
			t.Fatal(err)
		}
	}
	api.responses = map[string]string{"channel-send": crewReceipt()}
	api.statuses = map[string]int{"channel-send": http.StatusConflict}
	return app, fake, api
}

func runTranscript(t *testing.T, app *App, args ...string) string {
	t.Helper()
	var stdout string
	var runErr error
	stderr, _ := captureStderr(t, func() error {
		stdout, runErr = captureStdout(t, func() error { return app.Run(args) })
		return nil
	})
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = arg
		if strings.ContainsAny(arg, " ") {
			quoted[i] = `"` + arg + `"`
		}
	}
	out := "$ agenthail " + strings.Join(quoted, " ") + "\n" + stdout + stderr
	if runErr != nil {
		out += "error: " + runErr.Error() + "\n"
	}
	return out
}

func TestTheDocumentedChannelFanOutExampleIsRealOutput(t *testing.T) {
	app, fake, api := fanoutFixture(t)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"channel", "create", "crew"}) }); err != nil {
		t.Fatal(err)
	}
	var transcript strings.Builder
	for _, args := range [][]string{
		{"channel", "add", "crew", "@scout"},
		{"channel", "add", "crew", "@coder"},
		{"channel", "add", "crew", "@analyst"},
		{"channel", "add", "crew", "@reviewer"},
		{"channel", "add", "crew", "vps::@builder"},
	} {
		transcript.WriteString(runTranscript(t, app, args...))
	}
	transcript.WriteString(runTranscript(t, app, "channel", "send", "crew", "standup at 10"))
	got := transcript.String()
	if os.Getenv("AGENTHAIL_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(fanoutGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(fanoutGolden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("the fan-out example changed; rerun with AGENTHAIL_UPDATE_GOLDEN=1 and update the README and skill.\ngot:\n%s\nwant:\n%s", got, want)
	}
	for _, doc := range []string{"../../README.md", "../../skills/agenthail-operations/SKILL.md"} {
		text, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(text), got) {
			t.Errorf("%s does not carry the fan-out example verbatim", doc)
		}
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "channel-send" || actions[0]["channel"] != "crew" || actions[0]["message"] != "standup at 10" {
		t.Fatalf("actions=%v, want one channel-send to the daemon", actions)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("the CLI delivered %q itself", fake.sent)
	}
}

func TestChannelSendJSONReportsEveryMember(t *testing.T) {
	app, _, _ := fanoutFixture(t)
	output, err := captureStdout(t, func() error { return app.Run([]string{"channel", "send", "crew", "--json", "standup"}) })
	if err == nil || !strings.Contains(err.Error(), "1 failed") {
		t.Fatalf("a failed member must still fail the command: %v", err)
	}
	var body struct {
		Target     broadcast.Target      `json:"target"`
		Recipients []broadcast.Recipient `json:"recipients"`
		Sent       int                   `json:"sent"`
		Queued     int                   `json:"queued"`
		Submitted  int                   `json:"submitted"`
		Failed     int                   `json:"failed"`
		Error      any                   `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &body); err != nil {
		t.Fatalf("output is not one JSON object: %q (%v)", output, err)
	}
	if body.Target.Channel != "crew" || body.Sent != 1 || body.Queued != 2 || body.Submitted != 1 || body.Failed != 1 || len(body.Recipients) != 5 || body.Error != nil {
		t.Fatalf("body=%+v", body)
	}
}

func TestChannelSendNamesAHostItCouldNotReach(t *testing.T) {
	app, _, api := fanoutFixture(t)
	api.responses["channel-send"] = `{"ok":true,"target":{"name":"widget","projectId":"repo:github.com/example/widget","project":"widget"},"sent":1,"queued":0,"submitted":0,"failed":0,` +
		`"recipients":[{"sessionId":"s-scout","outcome":"sent","detail":"Sent to s-scout."}],` +
		`"hosts":[{"id":"` + vpsHostID + `","name":"vps","state":"held","recipients":0,"detail":"host vps is unreachable; held until it reconnects"}],"unreachable":["vps"]}`
	api.statuses = nil
	output, err := captureStdout(t, func() error { return app.Run([]string{"channel", "send", "#widget", "standup"}) })
	if err != nil || !strings.Contains(output, "[HELD] host vps: host vps is unreachable; held until it reconnects") || !strings.Contains(output, "held for unreachable host(s): vps") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestChannelSendFailsWhenAHostRefusesIt(t *testing.T) {
	app, _, api := fanoutFixture(t)
	api.responses["channel-send"] = `{"error":{"code":"conflict","message":"channel delivery: 0 sent"},"target":{"name":"widget","projectId":"repo:github.com/example/widget","project":"widget"},"sent":0,"queued":0,"submitted":0,"failed":0,"recipients":[],` +
		`"hosts":[{"id":"` + vpsHostID + `","name":"vps","state":"refused","recipients":0,"detail":"the sender is in project gadget, not widget","rule":"different_project"}],"unreachable":[]}`
	output, err := captureStdout(t, func() error { return app.Run([]string{"channel", "send", "#widget", "standup"}) })
	if err == nil || !strings.Contains(err.Error(), "1 host(s) refused it") || !strings.Contains(output, "[REFUSED] host vps: the sender is in project gadget") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestChannelListJSONNamesEachMemberAndItsHost(t *testing.T) {
	app, _, _ := fanoutFixture(t)
	for _, args := range [][]string{{"channel", "create", "crew"}, {"channel", "add", "crew", "@scout"}, {"channel", "add", "crew", "vps::@builder"}, {"channel", "create", "empty"}} {
		if _, err := captureStdout(t, func() error { return app.Run(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"channel", "list", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var channels []channelJSON
	if err := json.Unmarshal([]byte(output), &channels); err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if len(channels) != 2 || channels[0].Name != "crew" || channels[1].Name != "empty" || channels[1].Members == nil || len(channels[1].Members) != 0 {
		t.Fatalf("channels=%+v", channels)
	}
	members := map[string]channelMemberJSON{}
	for _, member := range channels[0].Members {
		members[member.Display] = member
	}
	if members["@scout"].SessionID != "s-scout" || members["@scout"].HostID != "" || members["vps::@builder"].HostID != vpsHostID || members["vps::@builder"].SessionID != vpsBuilder.ID {
		t.Fatalf("members=%+v", members)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"channel", "list", "--wide"}) }); err == nil {
		t.Fatal("an unknown list flag was accepted")
	}
}
