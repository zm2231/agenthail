package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestSendToABusyClaudeSessionRidesItsPeerSocketWithTheSender(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "dashboard.json"), []byte(`{"busyDelivery":"steer"}`), 0600); err != nil {
		t.Fatal(err)
	}
	app, r, fixture, posts := liveClaudeApp(t)
	fixture.Listen()
	fixture.Transcript(originalConversation, fixture.Home)
	fixture.RecordStatus(originalConversation, fixture.Home, liveBridge, "busy", "", time.Now())
	peers := claudetest.ListenPeerManager(t, fixture.Home)
	sender := surface.Session{ID: "sender", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := r.RegisterSession(sender); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", sender.ID)

	output, err := captureStdout(t, func() error {
		return app.Run([]string{"send", "--json", "claude:" + originalConversation, "handoff"})
	})
	var receipt struct {
		Evidence string `json:"evidence"`
	}
	if err != nil || json.Unmarshal([]byte(output), &receipt) != nil || receipt.Evidence != string(surface.EvidenceTransportAccepted) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	sends := peers.Sends()
	if len(sends) != 1 || sends[0].Source != sender.ID || sends[0].Message != "handoff" || sends[0].Target == "" {
		t.Fatalf("peer sends=%+v, want one framed from the sender", sends)
	}
	if posts.count("handoff") != 0 || r.QueueCount(originalConversation) != 0 {
		t.Fatalf("steered=%d queued=%d, want neither", posts.count("handoff"), r.QueueCount(originalConversation))
	}
	history, err := r.ListHistory(10, originalConversation)
	if err != nil || len(history) == 0 || history[0].Kind != "transport-accepted" || history[0].Message != "handoff" || history[0].SourceSessionID != sender.ID {
		t.Fatalf("history=%+v err=%v, want the message recorded from the sender", history, err)
	}
}

func TestQueueToABusyClaudeSessionRecordsAnExplicitQueue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app, r, fixture, posts := liveClaudeApp(t)
	fixture.Listen()
	fixture.Transcript(originalConversation, fixture.Home)
	fixture.RecordStatus(originalConversation, fixture.Home, liveBridge, "busy", "", time.Now())
	peers := claudetest.ListenPeerManager(t, fixture.Home)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"queue", "claude:" + originalConversation, "after the turn"})
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListQueue(false, nil)
	if err != nil || len(rows) != 1 || rows[0].Message != "after the turn" || rows[0].BusyDelivery != "queue" {
		t.Fatalf("rows=%+v err=%v, want one explicit queue", rows, err)
	}
	if len(peers.Sends()) != 0 || posts.count("after the turn") != 0 {
		t.Fatalf("sends=%+v posts=%d, want the message held", peers.Sends(), posts.count("after the turn"))
	}
}
