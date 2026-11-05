package daemon

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

type desktopNotices struct {
	mu    sync.Mutex
	shown []string
}

func (n *desktopNotices) capture(f *coordinatorFixture) {
	f.daemon.notifyDesktop = func(title, message, _, _, _ string, _ []surface.RequestAction) error {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.shown = append(n.shown, title+": "+message)
		return nil
	}
}

func (n *desktopNotices) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string{}, n.shown...)
}

func (f *coordinatorFixture) handover(t *testing.T) registry.CoordinatorHistoryEntry {
	t.Helper()
	history, err := f.r.CoordinatorHistory("project-fixture")
	if err != nil || len(history) == 0 || history[len(history)-1].Action != "replaced" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	return history[len(history)-1]
}

func (f *coordinatorFixture) queuedFor(t *testing.T, sessionID string) []registry.QueueRow {
	t.Helper()
	rows, err := f.r.ListQueue(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mine []registry.QueueRow
	for _, row := range rows {
		if row.SessionID == sessionID {
			mine = append(mine, row)
		}
	}
	return mine
}

func TestAPersonsSendThatReplacesTheLeadIsRecordedAndTellsThePersonOnce(t *testing.T) {
	f := newCoordinatorFixture(t)
	notices := &desktopNotices{}
	notices.capture(f)
	if code, body := f.send(t, "replace-by-person", goneCoordinator, "plan"); code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	entry := f.handover(t)
	if entry.ChangedBy != surface.PersonSessionID || !strings.HasPrefix(entry.Reason, "gone: conversation "+goneCoordinator+" no longer exists") || !strings.Contains(entry.Reason, "last seen") || entry.NotifySender {
		t.Fatalf("handover=%+v", entry)
	}
	f.daemon.announceReplacements(context.Background())
	f.daemon.announceReplacements(context.Background())
	shown := notices.all()
	if len(shown) != 1 || !strings.Contains(shown[0], "Lead replaced: @fixture-lead") || !strings.Contains(shown[0], "by the person") {
		t.Fatalf("desktop notices=%q, want one naming the lead and who replaced it", shown)
	}
	if unannounced, err := f.r.UnannouncedCoordinatorReplacements(); err != nil || len(unannounced) != 0 {
		t.Fatalf("unannounced=%+v err=%v", unannounced, err)
	}
	if open, err := f.r.OpenCoordinatorReplacements(); err != nil || len(open) != 1 || open[0].ID != entry.ID || !open[0].PersonNotified {
		t.Fatalf("open=%+v err=%v, want the handover waiting for the person, recorded as told", open, err)
	}
	if f.resumedGone(t) {
		t.Fatal("the gone lead was resumed to be told")
	}
}

func TestAQueuedMessageThatReplacesTheLeadTellsTheAgentThatSentIt(t *testing.T) {
	f := newCoordinatorFixture(t)
	notices := &desktopNotices{}
	notices.capture(f)
	const worker = "6a7b8c9d-0e1f-4a2b-8c3d-4e5f6a7b8c9d"
	if err := f.r.RegisterSession(surface.Session{ID: worker, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: orphanPID - 1, Status: surface.StatusIdle, Cwd: f.host.Cwd}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.QueueMessageWithOptions(goneCoordinator, "report from the worker", "", surface.SendOptions{SourceSessionID: worker}); err != nil {
		t.Fatal(err)
	}
	f.daemon.scanAndRelay(context.Background())
	entry := f.handover(t)
	if entry.ChangedBy != worker || !entry.NotifySender {
		t.Fatalf("handover=%+v, want it recorded as the worker's with a notice owed", entry)
	}
	f.daemon.announceReplacements(context.Background())
	f.daemon.announceReplacements(context.Background())
	told := f.queuedFor(t, worker)
	if len(told) != 1 || !strings.Contains(told[0].Message, "Your message to @fixture-lead started a new conversation, "+entry.SessionID) {
		t.Fatalf("worker queue=%+v, want one notice", told)
	}
	if used, err := f.r.DeliveryKeyUsed(replacementNoticeKey(entry.ID, "sender")); err != nil || !used {
		t.Fatalf("sender key used=%v err=%v", used, err)
	}
	if shown := notices.all(); len(shown) != 1 {
		t.Fatalf("desktop notices=%q", shown)
	}
}

func TestAQueuedMessageWithNoSenderIsReplacedByTheDaemonAndTellsNoAgent(t *testing.T) {
	f := newCoordinatorFixture(t)
	notices := &desktopNotices{}
	notices.capture(f)
	if err := f.r.QueueMessage(goneCoordinator, "held"); err != nil {
		t.Fatal(err)
	}
	f.daemon.scanAndRelay(context.Background())
	entry := f.handover(t)
	if entry.ChangedBy != coordinator.RestartedByDaemon {
		t.Fatalf("handover=%+v", entry)
	}
	before, err := f.r.ListQueue(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.daemon.announceReplacements(context.Background())
	after, err := f.r.ListQueue(false, nil)
	if err != nil || len(after) != len(before) {
		t.Fatalf("queue before=%d after=%d err=%v", len(before), len(after), err)
	}
	if shown := notices.all(); len(shown) != 1 || !strings.Contains(shown[0], "by the daemon") {
		t.Fatalf("desktop notices=%q", shown)
	}
}

func TestADaemonRestartedAfterThePushDoesNotTellThePersonAgain(t *testing.T) {
	f := newCoordinatorFixture(t)
	const next = "3c2b1a09-8f7e-4d6c-9b5a-4f3e2d1c0b9a"
	if err := f.r.RegisterSession(surface.Session{ID: next, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: orphanPID - 2, Status: surface.StatusIdle, Cwd: f.host.Cwd}); err != nil {
		t.Fatal(err)
	}
	spec, err := f.r.Coordinator("project-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.HandOverCoordinator(spec, goneCoordinator, next, registry.CoordinatorHandover{ChangedBy: coordinator.RestartedByDaemon, Reason: "gone: x"}); err != nil {
		t.Fatal(err)
	}
	entry := f.handover(t)
	first := &desktopNotices{}
	first.capture(f)
	f.daemon.notifyReplacement(context.Background(), entry)
	if err := f.r.MarkCoordinatorReplacementPersonNotified(entry.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	restarted := &coordinatorFixture{daemon: newTestDaemon(t, f.r, f.daemon.Surfaces), r: f.r}
	second := &desktopNotices{}
	second.capture(restarted)
	restarted.daemon.announceReplacements(context.Background())
	if shown := first.all(); len(shown) != 1 {
		t.Fatalf("first daemon notices=%q", shown)
	}
	if shown := second.all(); len(shown) != 0 {
		t.Fatalf("the restarted daemon told the person again: %q", shown)
	}
	if unannounced, err := f.r.UnannouncedCoordinatorReplacements(); err != nil || len(unannounced) != 0 {
		t.Fatalf("unannounced=%+v err=%v", unannounced, err)
	}
}

func TestASenderThatNoLongerExistsDoesNotHoldTheAnnouncement(t *testing.T) {
	f := newCoordinatorFixture(t)
	const next = "3c2b1a09-8f7e-4d6c-9b5a-4f3e2d1c0b9a"
	if err := f.r.RegisterSession(surface.Session{ID: next, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: orphanPID - 2, Status: surface.StatusIdle, Cwd: f.host.Cwd}); err != nil {
		t.Fatal(err)
	}
	spec, err := f.r.Coordinator("project-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.HandOverCoordinator(spec, goneCoordinator, next, registry.CoordinatorHandover{ChangedBy: "deleted-sender", Reason: "gone: x", NotifySender: true}); err != nil {
		t.Fatal(err)
	}
	f.daemon.announceReplacements(context.Background())
	if unannounced, err := f.r.UnannouncedCoordinatorReplacements(); err != nil || len(unannounced) != 0 {
		t.Fatalf("unannounced=%+v err=%v", unannounced, err)
	}
}

func TestAReplacedLeadThatStillExistsIsToldToStop(t *testing.T) {
	f := newCoordinatorFixture(t)
	if code, body := f.send(t, "replace", goneCoordinator, "plan"); code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	entry := f.handover(t)
	f.claude.Transcript(goneCoordinator, f.host.Cwd)
	peers := claudetest.ListenPeerManager(t, f.claude.Home)
	f.daemon.tellReplacedLead(context.Background(), entry)
	sends := peers.Sends()
	if len(sends) == 0 {
		t.Fatal("the old lead was not told to stop")
	}
	for _, send := range sends {
		if send.Source != surface.SystemSessionID || !strings.HasPrefix(send.Message, "[agenthail] You are no longer coordinator @fixture-lead") || !strings.Contains(send.Message, "end your turn") || !strings.Contains(send.Message, entry.SessionID) {
			t.Fatalf("sends=%+v, want Agenthail to tell the old lead to stop", sends)
		}
	}
	if queued := f.queuedFor(t, entry.SessionID); len(queued) != 0 {
		t.Fatalf("the old lead's notice reached the new lead: %+v", queued)
	}
}
