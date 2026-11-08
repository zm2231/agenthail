package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/surface"
)

const fanoutGolden = "testdata/channel_fanout_example.txt"

type fanoutSurface struct {
	*cliSurface
	results map[string]*surface.SendResult
	errs    map[string]error
}

func (f *fanoutSurface) Send(_ context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	f.sent = append(f.sent, session.ID+": "+message)
	if err := f.errs[session.ID]; err != nil {
		return nil, err
	}
	if result := f.results[session.ID]; result != nil {
		return result, nil
	}
	return &surface.SendResult{UUID: "turn-" + session.ID, Accepted: true}, nil
}

func fanoutFixture(t *testing.T) (*App, *fanoutSurface) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	app, _, fake, _ := federatedCLIFixture(t, true)
	writable := func(id string, status surface.SessionStatus) surface.Session {
		return surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: status}
	}
	fake.sessions = map[string]surface.Session{
		"s-scout":    writable("s-scout", surface.StatusIdle),
		"s-coder":    writable("s-coder", surface.StatusBusy),
		"s-analyst":  writable("s-analyst", surface.StatusIdle),
		"s-reviewer": writable("s-reviewer", surface.StatusIdle),
	}
	fanout := &fanoutSurface{
		cliSurface: fake,
		results:    map[string]*surface.SendResult{"s-coder": {UUID: "turn-coder", Accepted: false}},
		errs:       map[string]error{"s-analyst": surface.DeliveryOutcomeUnknown(errors.New("the acknowledgement was lost"))},
	}
	app.Surfaces[0].Surface = fanout
	for _, name := range []string{"scout", "coder", "analyst", "reviewer"} {
		if _, err := captureStdout(t, func() error { return app.Run([]string{"identify", "codex:s-" + name, name}) }); err != nil {
			t.Fatal(err)
		}
	}
	return app, fanout
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
	app, fanout := fanoutFixture(t)
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
	reviewer := fanout.sessions["s-reviewer"]
	reviewer.Transport = "readOnly"
	fanout.sessions["s-reviewer"] = reviewer
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
	if len(fanout.sent) != 3 {
		t.Fatalf("sent=%q, want the idle, busy and uncertain members tried and the read-only one refused before sending", fanout.sent)
	}
}

func TestChannelSendJSONReportsEveryMember(t *testing.T) {
	app, fanout := fanoutFixture(t)
	for _, args := range [][]string{{"channel", "create", "crew"}, {"channel", "add", "crew", "@scout"}, {"channel", "add", "crew", "@coder"}, {"channel", "add", "crew", "@reviewer"}, {"channel", "add", "crew", "vps::@builder"}} {
		if _, err := captureStdout(t, func() error { return app.Run(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	reviewer := fanout.sessions["s-reviewer"]
	reviewer.Transport = "readOnly"
	fanout.sessions["s-reviewer"] = reviewer
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
	}
	if err := json.Unmarshal([]byte(output), &body); err != nil {
		t.Fatalf("output is not one JSON object: %q (%v)", output, err)
	}
	if body.Target.Channel != "crew" || body.Sent != 1 || body.Queued != 2 || body.Submitted != 0 || body.Failed != 1 || len(body.Recipients) != 4 {
		t.Fatalf("body=%+v", body)
	}
	outcomes := map[string]broadcast.Recipient{}
	for _, recipient := range body.Recipients {
		outcomes[recipient.SessionID] = recipient
	}
	if outcomes["s-scout"].Outcome != "sent" || outcomes["s-coder"].Outcome != "queued" || outcomes["s-reviewer"].Outcome != "failed" || !strings.Contains(outcomes["s-reviewer"].Detail, "read only") {
		t.Fatalf("outcomes=%+v", outcomes)
	}
	if remote := outcomes[vpsBuilder.ID]; remote.Outcome != "queued" || remote.HostID != vpsHostID || remote.Detail != "held for its host" {
		t.Fatalf("paired-host member=%+v", remote)
	}
}

func TestChannelListJSONNamesEachMemberAndItsHost(t *testing.T) {
	app, _ := fanoutFixture(t)
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
