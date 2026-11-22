package ledger_test

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

const holder = "gone-worker"

func heldBy(t *testing.T, s *ledger.Store, texts ...string) {
	t.Helper()
	items(t, s, texts...)
	for i := range texts {
		work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W" + string(rune('1'+i)), SessionID: holder})
	}
}

func takeover(then, verdict string) *ledger.Takeover {
	return &ledger.Takeover{Then: then, Holder: holder, Verdict: verdict}
}

func lastEntry(item ledger.WorkItem) ledger.WorkEntry {
	return item.Timeline[len(item.Timeline)-1]
}

func TestAnotherSessionReleasesOrSubmitsWorkWhoseHolderIsGoneAndTheLogSaysWhoAndWhy(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "add the index", "wire the endpoint")

	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "worker archived", Takeover: takeover(ledger.TakeoverRelease, "")}, "was not judged")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Takeover: takeover(ledger.TakeoverRelease, ledger.HolderGone)}, "takeover needs --reason")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "x", Takeover: takeover("finish", ledger.HolderGone)}, "--then release, submit or done")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "x", Takeover: &ledger.Takeover{Then: ledger.TakeoverRelease, Holder: "someone-else", Verdict: ledger.HolderGone}}, "check it again")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", SessionID: holder, Reason: "x", Takeover: takeover(ledger.TakeoverRelease, ledger.HolderGone)}, "you hold widget-W1")

	released := work(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "its session was archived mid-task", Takeover: takeover(ledger.TakeoverRelease, ledger.HolderArchived)})
	if released.Item.State != ledger.WorkOpen || released.Item.Assignee != "" {
		t.Fatalf("released = %+v", released.Item)
	}
	entry := lastEntry(released.Item)
	if entry.Actor != "s1" || !strings.Contains(entry.Text, "taken over from "+holder+" (archived)") || !strings.Contains(entry.Text, "its session was archived mid-task") {
		t.Fatalf("release entry = %+v", entry)
	}

	submitted := work(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W2", Reason: "done before its host was reset", Takeover: takeover(ledger.TakeoverSubmit, ledger.HolderGone)})
	if submitted.Item.State != ledger.WorkInReview || submitted.Item.Assignee != "s1" {
		t.Fatalf("submitted = %+v", submitted.Item)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W2"}, "another session reviews it")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W2", SessionID: "reviewer"})
}

func TestAGoneHoldersItemIsDoneOnlyByAMergedPRTheWatcherLogged(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "add the index")
	url := "https://github.com/acme/widget/pull/12"
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", SessionID: holder, Text: "PR merged: " + url})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "merged, holder gone", Takeover: takeover(ledger.TakeoverDone, ledger.HolderGone)}, "no \"PR merged:\" line from the PR watcher")

	work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", SessionID: holder, Text: "PR merged: " + url, Source: ledger.WorkSourcePRWatcher})
	done := work(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "merged, holder gone", Takeover: takeover(ledger.TakeoverDone, ledger.HolderGone)})
	if done.Item.State != ledger.WorkDone || done.Item.Assignee != holder {
		t.Fatalf("done = %+v", done.Item)
	}
	if entry := lastEntry(done.Item); entry.Actor != "s1" || !strings.Contains(entry.Text, "PR merged: "+url) {
		t.Fatalf("done entry = %+v", entry)
	}
}

func TestOnlyThePersonsWordsAndForceTakeWorkFromALiveOrUnansweredHolder(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "before the claim")
	heldBy(t, s, "add the index", "wire the endpoint")
	for _, verdict := range []string{ledger.HolderLive, ledger.HolderUnknown} {
		workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "stuck", Takeover: takeover(ledger.TakeoverRelease, verdict)}, "the person takes it over with --utterance")
	}
	capture(t, s, project, "claude-prompt:3", "take the index back from that worker")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "stuck", UtteranceID: "claude-prompt:3", Takeover: takeover(ledger.TakeoverRelease, ledger.HolderLive)}, "the person takes it over with --utterance")
	forced := takeover(ledger.TakeoverRelease, ledger.HolderLive)
	forced.Force = true
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "stuck", Takeover: forced}, "the person takes it over with --utterance")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "stuck", UtteranceID: "claude-prompt:2", Takeover: forced}, "was said before widget-W1 was claimed")

	released := work(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "stuck", UtteranceID: "claude-prompt:3", Takeover: forced})
	if released.Item.State != ledger.WorkOpen || !strings.Contains(lastEntry(released.Item).Text, "(live, forced by the person)") {
		t.Fatalf("forced release = %+v", released.Item)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W2", Reason: "stuck", UtteranceID: "claude-prompt:3", Takeover: forced}, "one utterance decides one item")
}
