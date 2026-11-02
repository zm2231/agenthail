package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type noticeFixture struct {
	d       *Daemon
	reg     *registry.Registry
	repo    string
	worker  surface.Session
	request surface.Request
	waiting surface.Waiting
}

func newNoticeFixture(t *testing.T) noticeFixture {
	t.Helper()
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	repo := widgetRepo(t, "https://github.com/example/widget.git")
	sessions := map[string]surface.Session{}
	for _, id := range []string{"worker", "lead", "spawner"} {
		session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, Source: "vscode", Transport: "desktop", Cwd: repo, LastActive: time.Now()}
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		sessions[id] = session
	}
	if err := reg.SetAlias("builder", "worker"); err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordSessionOrigin(registry.OriginOf("worker", "spawner")); err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{sessions: sessions, observations: map[string]*surface.TurnObservation{}, accepted: true}
	d := newTestDaemon(t, reg, []surface.Surface{fake})
	waiting := surface.Waiting{Kind: surface.WaitingApproval, ID: "approve-1", Answerable: true}
	request := surface.Request{ID: "approve-1", Kind: surface.WaitingApproval, Tool: "Bash", Description: "rm -rf build"}
	return noticeFixture{d: d, reg: reg, repo: repo, worker: sessions["worker"], request: request, waiting: waiting}
}

func (f noticeFixture) setLead(t *testing.T, sessionID string) {
	t.Helper()
	spec := registry.CoordinatorSpec{ProjectID: "repo:github.com/example/widget", ProjectRoot: f.repo, Name: "widget-lead", Surface: "codex", Cwd: f.repo}
	if _, err := f.reg.SetCoordinator(spec, registry.CoordinatorChange{SessionID: sessionID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
}

func (f noticeFixture) notices(t *testing.T) []registry.QueueRow {
	t.Helper()
	rows, err := f.reg.ListQueue(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var notices []registry.QueueRow
	for _, row := range rows {
		if row.SourceSessionID == f.worker.ID {
			notices = append(notices, row)
		}
	}
	return notices
}

func TestAWorkersPermissionPromptGoesToItsLeadOnceWithHowToAnswer(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	f.d.noticeWaiting(context.Background(), f.worker, f.waiting, f.request, true)
	f.d.noticeWaiting(context.Background(), f.worker, f.waiting, f.request, true)
	notices := f.notices(t)
	if len(notices) != 1 || notices[0].SessionID != "lead" || notices[0].Status != "pending" {
		t.Fatalf("notices=%+v", notices)
	}
	for _, want := range []string{"Bash: rm -rf build", "agenthail answer @builder", "--request approve-1", "person was notified"} {
		if !strings.Contains(notices[0].Message, want) {
			t.Fatalf("the notice %q does not say %q", notices[0].Message, want)
		}
	}
}

func TestAPromptGoesToTheSpawnerWhenTheProjectHasNoLead(t *testing.T) {
	f := newNoticeFixture(t)
	f.d.noticeWaiting(context.Background(), f.worker, f.waiting, f.request, true)
	if notices := f.notices(t); len(notices) != 1 || notices[0].SessionID != "spawner" {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestALeadsOwnPromptGoesToItsSpawner(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "worker")
	f.d.noticeWaiting(context.Background(), f.worker, f.waiting, f.request, true)
	if notices := f.notices(t); len(notices) != 1 || notices[0].SessionID != "spawner" {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestAPromptWithNoLeadAndNoSpawnerTellsNoAgent(t *testing.T) {
	f := newNoticeFixture(t)
	orphan := f.worker
	orphan.ID = "lead"
	f.d.noticeWaiting(context.Background(), orphan, f.waiting, f.request, true)
	rows, err := f.reg.ListQueue(true, nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestAnAnsweredPromptWithdrawsItsPendingNoticeAndIsNotNoticedAgain(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	f.d.noticeWaiting(context.Background(), f.worker, f.waiting, f.request, true)
	f.d.withdrawWaitingNow(context.Background(), f.worker.ID, f.waiting)
	notices := f.notices(t)
	if len(notices) != 1 || notices[0].Status != "canceled" {
		t.Fatalf("notices=%+v", notices)
	}
	f.d.noticeWaiting(context.Background(), f.worker, f.waiting, f.request, true)
	if again := f.notices(t); len(again) != 1 || again[0].Status != "canceled" {
		t.Fatalf("a withdrawn notice was sent again: %+v", again)
	}
}

func TestAPromptAgenthailCannotReadStillTellsTheLeadWhatIsWaiting(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	f.d.noticeWaiting(context.Background(), f.worker, f.waiting, surface.Request{}, false)
	notices := f.notices(t)
	if len(notices) != 1 || !strings.Contains(notices[0].Message, "needs your approval") {
		t.Fatalf("notices=%+v", notices)
	}
}
