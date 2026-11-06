package daemon

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func inboxRow(kind string, edit func(*dashboardSession)) dashboardSession {
	row := dashboardSession{ID: "s", Display: &SessionDisplay{Kind: kind}}
	if edit != nil {
		edit(&row)
	}
	return row
}

func TestEachRowLandsInOneInboxSection(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	later := &dashboardSnooze{SnoozedAt: now.Add(-time.Hour), Until: now.Add(time.Hour), Wake: registry.SnoozeWakeTime}
	past := &dashboardSnooze{SnoozedAt: now.Add(-2 * time.Hour), Until: now.Add(-time.Minute), Wake: registry.SnoozeWakeTime}
	asks := &SessionAttention{Kind: "request"}
	cases := []struct {
		name string
		row  dashboardSession
		want string
	}{
		{"idle", inboxRow("idle", nil), InboxDone},
		{"offline", inboxRow("offline", nil), InboxDone},
		{"working", inboxRow("working", nil), InboxRunning},
		{"finishing", inboxRow("finishing", nil), InboxRunning},
		{"queued", inboxRow("queued", nil), InboxRunning},
		{"waiting on the person", inboxRow("waiting", func(r *dashboardSession) { r.Attention = asks }), InboxNeedsYou},
		{"snoozed until later", inboxRow("idle", func(r *dashboardSession) { r.Snooze = later }), InboxSnoozed},
		{"snooze has passed", inboxRow("idle", func(r *dashboardSession) { r.Snooze = past }), InboxDone},
		{"a request outranks a snooze", inboxRow("waiting", func(r *dashboardSession) { r.Snooze, r.Attention = later, asks }), InboxNeedsYou},
		{"archived outranks a request", inboxRow("waiting", func(r *dashboardSession) { r.Archived, r.Attention = true, asks }), InboxArchived},
		{"no display yet", dashboardSession{ID: "s"}, InboxDone},
	}
	for _, c := range cases {
		if got := sessionInbox(c.row, now); got != c.want {
			t.Errorf("%s: inbox %q, want %q", c.name, got, c.want)
		}
	}
}

func TestASnoozeUntilFinishedLastsOnlyForTheTurnItWasSetIn(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	snooze := &dashboardSnooze{SnoozedAt: now.Add(-time.Minute), Wake: registry.SnoozeWakeFinished}
	turn := func(kind string, busySince time.Time) dashboardSession {
		return inboxRow(kind, func(r *dashboardSession) {
			r.Snooze, r.Activity = snooze, &surface.Activity{BusySince: busySince}
		})
	}
	if got := sessionInbox(turn("working", now.Add(-5*time.Minute)), now); got != InboxSnoozed {
		t.Fatalf("the snoozed turn shows as %q", got)
	}
	if got := sessionInbox(turn("idle", time.Time{}), now); got != InboxDone {
		t.Fatalf("a finished turn shows as %q", got)
	}
	if got := sessionInbox(turn("working", now), now); got != InboxRunning {
		t.Fatalf("a later turn stayed snoozed: %q", got)
	}
}

func TestTheViewIsRereadWhenASnoozeEnds(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	until := now.Add(90 * time.Minute)
	row := inboxRow("idle", func(r *dashboardSession) {
		r.Snooze = &dashboardSnooze{SnoozedAt: now, Until: until, Wake: registry.SnoozeWakeTime}
	})
	plan := newSessionViewPlan([]dashboardSession{row}, "all", SessionViewFilters{}, now)
	if plan.view.RefreshAt == nil || !plan.view.RefreshAt.Equal(until) {
		t.Fatalf("refreshAt %v, want the snooze's end %v", plan.view.RefreshAt, until)
	}
}

func TestAPinnedSessionStaysCurrentWhenItIsNotRunning(t *testing.T) {
	now := time.Now()
	row := dashboardSession{ID: "s", Status: surface.StatusOffline, Display: &SessionDisplay{Kind: "offline"}}
	if presentationCurrent(&row) || catalogScopeMatches("current", row) {
		t.Fatal("an unpinned offline session counted as current")
	}
	row.PinnedAt = now
	if !presentationCurrent(&row) || !catalogScopeMatches("current", row) || !presentationMatches(&row, "recent", now) {
		t.Fatal("a pinned session dropped out of the current view")
	}
	row.Archived = true
	if presentationCurrent(&row) || catalogScopeMatches("current", row) {
		t.Fatal("an archived pinned session counted as current")
	}
}

func snoozeAction(t *testing.T, d *Daemon, body string) (int, map[string]json.RawMessage) {
	t.Helper()
	w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", body)
	var answer map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &answer)
	return w.Code, answer
}

func TestSnoozeAndWakeHandBackThePreviousSnoozeForUndo(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	until := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Millisecond)
	code, answer := snoozeAction(t, d, `{"action":"session-snooze","sessionId":"to","until":"`+until.Format(time.RFC3339Nano)+`"}`)
	if code != http.StatusOK || string(answer["previous"]) != "null" {
		t.Fatalf("first snooze: %d %s", code, answer)
	}
	row := keptRow(t, d, "to")
	if row.InboxSection != InboxSnoozed || row.Snooze == nil || !row.Snooze.Until.Equal(until) || row.Snooze.Wake != registry.SnoozeWakeTime {
		t.Fatalf("snoozed row: section %q snooze %+v", row.InboxSection, row.Snooze)
	}
	code, answer = snoozeAction(t, d, `{"action":"session-wake","sessionId":"to"}`)
	var previous dashboardSnooze
	if code != http.StatusOK || json.Unmarshal(answer["previous"], &previous) != nil || !previous.Until.Equal(until) {
		t.Fatalf("wake: %d %s", code, answer)
	}
	if row := keptRow(t, d, "to"); row.InboxSection == InboxSnoozed || row.Snooze != nil {
		t.Fatalf("woken row: section %q snooze %+v", row.InboxSection, row.Snooze)
	}
}

func TestASnoozeThatCannotHoldIsRefused(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for name, body := range map[string]string{
		"no until or wake":    `{"action":"session-snooze","sessionId":"to"}`,
		"until in the past":   `{"action":"session-snooze","sessionId":"to","until":"` + past + `"}`,
		"both until and wake": `{"action":"session-snooze","sessionId":"to","until":"` + future + `","wake":"finished"}`,
		"finished when idle":  `{"action":"session-snooze","sessionId":"to","wake":"finished"}`,
	} {
		if code, answer := snoozeAction(t, d, body); code == http.StatusOK {
			t.Errorf("%s was accepted: %s", name, answer)
		}
	}
	if err := r.ArchiveSession("to", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := snoozeAction(t, d, `{"action":"session-snooze","sessionId":"to","until":"`+future+`"}`); code != http.StatusConflict {
		t.Fatalf("snoozing an archived session answered %d", code)
	}
	marks, err := r.SessionMarks()
	if err != nil || marks["to"].Snooze.Set() {
		t.Fatalf("a refused snooze was stored: %+v %v", marks["to"], err)
	}
}
