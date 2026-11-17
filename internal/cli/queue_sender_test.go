package cli

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAQueuedMessageNamesTheAgentThatQueuedIt(t *testing.T) {
	sessions := map[string]surface.Session{
		"busy":   {ID: "busy", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusBusy},
		"worker": {ID: "worker", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle},
	}
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude, sessions: sessions})
	if err := r.RecordObservedSession(sessions["worker"]); err != nil {
		t.Fatal(err)
	}
	queue := func(message string, intentID int64) (string, string) {
		t.Helper()
		if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", "claude:busy", message}) }); err != nil {
			t.Fatal(err)
		}
		rows, err := r.ListQueue(false, app.adapters())
		if err != nil {
			t.Fatal(err)
		}
		intent, err := r.DeliveryIntent(intentID)
		if err != nil {
			t.Fatalf("queued %q without a delivery intent: %v", message, err)
		}
		for _, row := range rows {
			if row.Message == message && intent.QueueID == row.ID {
				return row.SourceSessionID, intent.SenderSessionID
			}
		}
		t.Fatalf("%q was not queued under delivery intent %d", message, intentID)
		return "", ""
	}
	if source, sender := queue("from a shell", 1); source != "" || sender != surface.SourcelessSender("") {
		t.Fatalf("a message queued outside any agent session names sender %q (intent %q)", source, sender)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	if source, sender := queue("from the worker", 2); source != "worker" || sender != "worker" {
		t.Fatalf("a message an agent queued names sender %q (intent %q), want worker", source, sender)
	}
}

func TestARemoteQueueCarriesTheAgentThatQueuedIt(t *testing.T) {
	for _, identity := range surface.CallerIdentity {
		t.Setenv(identity.Variable, "")
	}
	app, r, _, api := federatedCLIFixture(t, true)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", "vps::@builder", "from the CLI"}) }); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordObservedSession(surface.Session{ID: "worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", "vps::@builder", "from the worker"}) }); err != nil {
		t.Fatal(err)
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["busyDelivery"] != "queue" || actions[1]["busyDelivery"] != "queue" {
		t.Fatalf("actions=%v, want two queued sends", actions)
	}
	if _, hasSource := actions[0]["sourceSessionId"]; hasSource {
		t.Fatalf("a queue run outside any agent session carried a source: %v", actions[0])
	}
	if actions[1]["sourceSessionId"] != "worker" || !strings.Contains(actions[1]["message"].(string), "from the worker") {
		t.Fatalf("the remote host cannot tell which agent queued the message: %v", actions[1])
	}
}

func TestRemovingAQueuedMessageAsksForTheReachOfTheCancelItSends(t *testing.T) {
	server := &scopeServer{accepted: `{"targetId":"target"}`}
	app, _ := scopedFixture(t, server)
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", "codex:target", "from the worker"}) }); err != nil {
		t.Fatal(err)
	}
	rows, err := app.Registry.ListQueue(false, app.adapters())
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	server.refusal = `{"scope":true,"error":{"code":"capability_missing","message":"Cancel queued message #1 is for the person."}}`
	if err := app.Registry.RecordObservedSession(surface.Session{ID: "lead", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "lead")
	if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", "rm", strconv.FormatInt(rows[0].ID, 10)}) }); err == nil || !strings.Contains(err.Error(), "refused (capability_missing)") {
		t.Fatalf("err=%v", err)
	}
	asked := server.requests()
	removal := asked[len(asked)-1]
	want := fmt.Sprintf(`{"action":"queue-cancel","queueId":%d}`, rows[0].ID)
	if removal.SessionID != "lead" || removal.Action != "operator" || removal.Payload == nil || removal.Payload.API != "action" || string(removal.Payload.Body) != want {
		t.Fatalf("the removal asked for %+v, want the reach for the cancel it would send, %s", removal, want)
	}
	if rows, err := app.Registry.ListQueue(false, app.adapters()); err != nil || len(rows) != 1 || rows[0].Status != "pending" {
		t.Fatalf("a refused removal changed the queue: %+v %v", rows, err)
	}
}
