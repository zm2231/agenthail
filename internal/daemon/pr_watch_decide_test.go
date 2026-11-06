package daemon

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
)

type watchRig struct {
	t    *testing.T
	d    *Daemon
	fake *fakeGitHub
	ref  github.Ref
	now  time.Time
}

func newWatchRig(t *testing.T) *watchRig {
	d, r, _, _, _ := daemonFixture(t)
	rig := &watchRig{t: t, d: d, fake: &fakeGitHub{viewer: "agent"}, ref: linkWatched(t, r, "to", 42), now: time.Now()}
	d.github = rig.fake
	return rig
}

func (w *watchRig) tick() {
	w.t.Helper()
	w.now = w.now.Add(prSweepEvery)
	if err := w.d.watchPRs(w.t.Context(), w.now); err != nil {
		w.t.Fatal(err)
	}
}

func (w *watchRig) watching() bool {
	w.t.Helper()
	pr, found, err := w.d.Registry.SessionPR("to", acmeApp.String(), 42)
	if err != nil || !found {
		w.t.Fatalf("pull request row: found=%t err=%v", found, err)
	}
	return pr.Watch
}

func (w *watchRig) personSets(op string) {
	w.t.Helper()
	if code, answer := workAction(w.t, w.d, map[string]any{"action": "pr", "sessionId": "to", "op": op, "pr": w.ref.URL()}); code != http.StatusOK {
		w.t.Fatalf("%s: %d %v", op, code, answer)
	}
}

func TestCommentsWakeUntilTenInARowThenTheWatchStopsUntilRewatched(t *testing.T) {
	w := newWatchRig(t)
	status := openStatus(w.ref, "aaaaaaa1111")
	status.Remarks = []github.Remark{{ID: "old", Kind: github.RemarkComment, Author: "pat", At: w.now}}
	w.fake.setStatus(status)
	w.tick()
	if wakes := wakesFor(t, w.d.Registry, "to"); len(wakes) != 0 {
		t.Fatalf("the first read woke for old comments: %q", wakes)
	}

	comment := func(i int) {
		at := w.now.Add(time.Duration(i) * time.Second)
		status.Remarks = append(status.Remarks,
			github.Remark{ID: fmt.Sprint("mine", i), Kind: github.RemarkComment, Author: "agent", At: at},
			github.Remark{ID: fmt.Sprint("c", i), Kind: github.RemarkComment, Author: "pat", At: at})
		w.fake.setStatus(status)
		w.tick()
	}
	for i := 1; i <= prCommentWakeLimit; i++ {
		comment(i)
		wakes := wakesFor(t, w.d.Registry, "to")
		if len(wakes) != i || !strings.Contains(wakes[i-1], "pat commented.") && !strings.Contains(wakes[i-1], "pat commented;") {
			t.Fatalf("after comment %d wakes = %q", i, wakes)
		}
		if i < prCommentWakeLimit && !w.watching() {
			t.Fatalf("the watch stopped after %d comment wakes", i)
		}
	}
	if wakes := wakesFor(t, w.d.Registry, "to"); w.watching() || !strings.Contains(wakes[len(wakes)-1], "watch stopped") {
		t.Fatalf("after %d comment wakes: watching=%t last=%q", prCommentWakeLimit, w.watching(), wakes[len(wakes)-1])
	}

	w.personSets("watch")
	comment(prCommentWakeLimit + 1)
	if !w.watching() {
		t.Fatal("a re-watched pull request stopped on its next comment")
	}
}

func TestAClosedPullRequestWakesOnceAndStopsItsWatch(t *testing.T) {
	w := newWatchRig(t)
	w.fake.setStatus(openStatus(w.ref, "aaaaaaa1111"))
	w.tick()
	closed := openStatus(w.ref, "aaaaaaa1111")
	closed.State = "closed"
	w.fake.setStatus(closed)
	w.tick()
	w.tick()
	if wakes := wakesFor(t, w.d.Registry, "to"); len(wakes) != 1 || !strings.Contains(wakes[0], "closed without merging") || w.watching() {
		t.Fatalf("closed: watching=%t wakes=%q", w.watching(), wakes)
	}
}

func TestARewatchDuringAReadOutlivesThatRead(t *testing.T) {
	w := newWatchRig(t)
	w.fake.setStatus(openStatus(w.ref, "aaaaaaa1111"))
	w.tick()
	closed := openStatus(w.ref, "aaaaaaa1111")
	closed.State = "closed"
	w.fake.setStatus(closed)
	w.fake.duringRead = func() {
		w.fake.duringRead = nil
		w.personSets("unwatch")
		w.personSets("watch")
	}
	w.tick()
	if !w.watching() {
		t.Fatal("a read begun before the re-watch turned it off")
	}
	if wakes := wakesFor(t, w.d.Registry, "to"); len(wakes) != 0 {
		t.Fatalf("the stale read woke: %q", wakes)
	}
	w.tick()
	if wakes := wakesFor(t, w.d.Registry, "to"); len(wakes) != 1 || w.watching() {
		t.Fatalf("the next read: watching=%t wakes=%q", w.watching(), wakes)
	}
}

func TestAReadThatMissesAPullRequestDuringARewatchCountsNothing(t *testing.T) {
	w := newWatchRig(t)
	rewatch := func() {
		w.personSets("unwatch")
		w.personSets("watch")
	}
	w.fake.duringRead = rewatch
	for range registry.PRReadFailureLimit {
		w.tick()
	}
	pr, _, _ := w.d.Registry.SessionPR("to", acmeApp.String(), 42)
	if pr.ReadFailures != 0 || w.d.prProblem(pr) != "" {
		t.Fatalf("reads begun before each re-watch counted failures=%d problem=%q", pr.ReadFailures, w.d.prProblem(pr))
	}
}

func TestAPullRequestGitHubStopsReturningShowsItsProblem(t *testing.T) {
	w := newWatchRig(t)
	drain := func() bool {
		select {
		case <-w.d.catalogNudge:
			return true
		default:
			return false
		}
	}
	drain()
	for i := 1; i < registry.PRReadFailureLimit; i++ {
		w.tick()
		if drain() {
			t.Fatalf("read failure %d republished the catalog with nothing new to show", i)
		}
	}
	w.tick()
	if !drain() {
		t.Fatal("the read that made the problem visible did not republish the catalog")
	}
	prs, _ := w.d.Registry.PRsForSession("to")
	if views := w.d.prViews(prs); !strings.Contains(views[0].Problem, "has not returned") {
		t.Fatalf("views = %+v", views)
	}
	w.fake.setStatus(openStatus(w.ref, "aaaaaaa1111"))
	w.tick()
	prs, _ = w.d.Registry.PRsForSession("to")
	if views := w.d.prViews(prs); views[0].Problem != "" || !drain() {
		t.Fatalf("a returned pull request kept its problem or was not republished: %+v", views)
	}
}
