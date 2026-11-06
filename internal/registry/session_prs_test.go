package registry

import (
	"context"
	"testing"
	"time"
)

func TestAnUnlinkedPullRequestStaysUnlinkedUntilLinkedExplicitly(t *testing.T) {
	r := openTestRegistry(t)
	at := time.UnixMilli(1_800_000_000_000)
	pr := SessionPR{SessionID: "s1", Repo: "github.com/acme/app", Number: 42, URL: "https://github.com/acme/app/pull/42", Source: PRSourceCreated}
	if linked, err := r.LinkPR(pr, false, at); err != nil || !linked {
		t.Fatalf("first link = %v, %v", linked, err)
	}
	if linked, err := r.LinkPR(pr, true, at); err != nil || linked {
		t.Fatalf("linking again = %v, %v; want already linked", linked, err)
	}
	if unlinked, err := r.UnlinkPR("s1", pr.Repo, 42); err != nil || !unlinked {
		t.Fatalf("unlink = %v, %v", unlinked, err)
	}
	pr.Source = PRSourceBranch
	if linked, err := r.LinkPR(pr, false, at); err != nil || linked {
		t.Fatalf("found again after unlink = %v, %v; want it to stay unlinked", linked, err)
	}
	if prs, err := r.PRsForSession("s1"); err != nil || len(prs) != 0 {
		t.Fatalf("PRsForSession after unlink = %+v, %v", prs, err)
	}
	pr.Source = PRSourceAgent
	if linked, err := r.LinkPR(pr, true, at); err != nil || !linked {
		t.Fatalf("explicit link after unlink = %v, %v", linked, err)
	}
	got, found, err := r.SessionPR("s1", pr.Repo, 42)
	if err != nil || !found || got.Source != PRSourceAgent || !got.Watch {
		t.Fatalf("SessionPR = %+v, %v, %v", got, found, err)
	}
}

func TestAWatchReadMovesTheSessionViewKeyOnlyWhenWhatItShowsChanges(t *testing.T) {
	r := openTestRegistry(t)
	at := time.UnixMilli(1_800_000_000_000)
	if _, err := r.LinkPR(SessionPR{SessionID: "s1", Repo: "github.com/acme/app", Number: 42, URL: "https://github.com/acme/app/pull/42", Source: PRSourceAgent}, true, at); err != nil {
		t.Fatal(err)
	}
	read := func(checks string) (moved bool) {
		t.Helper()
		pr, _, err := r.SessionPR("s1", "github.com/acme/app", 42)
		if err != nil {
			t.Fatal(err)
		}
		before, err := r.ReadCatalogPresentationKey(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		pr.State, pr.Checks = "open", checks
		at = at.Add(time.Minute)
		if recorded, err := r.RecordPRRead(pr, pr.Wakes, at); err != nil || !recorded {
			t.Fatalf("read = %v, %v", recorded, err)
		}
		after, err := r.ReadCatalogPresentationKey(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return after != before
	}
	if !read("passing") {
		t.Fatal("a read that changed the checks kept the view key")
	}
	if read("passing") {
		t.Fatal("a read that changed nothing shown moved the view key")
	}
}

func TestReadFailuresMoveTheSessionViewKeyOnlyWhenTheyReachTheLimit(t *testing.T) {
	r := openTestRegistry(t)
	at := time.UnixMilli(1_800_000_000_000)
	if _, err := r.LinkPR(SessionPR{SessionID: "s1", Repo: "github.com/acme/app", Number: 42, URL: "https://github.com/acme/app/pull/42", Source: PRSourceAgent}, true, at); err != nil {
		t.Fatal(err)
	}
	moved := func(write func(SessionPR) (bool, error)) bool {
		t.Helper()
		pr, _, err := r.SessionPR("s1", "github.com/acme/app", 42)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := r.ReadCatalogPresentationKey(context.Background())
		if recorded, err := write(pr); err != nil || !recorded {
			t.Fatalf("write = %v, %v", recorded, err)
		}
		after, _ := r.ReadCatalogPresentationKey(context.Background())
		return after != before
	}
	fail := func(pr SessionPR) (bool, error) { return r.RecordPRReadFailure("s1", pr.Repo, pr.Number, pr.Wakes) }
	for i := 1; i < PRReadFailureLimit; i++ {
		if moved(fail) {
			t.Fatalf("read failure %d moved the key with nothing new to show", i)
		}
	}
	if !moved(fail) {
		t.Fatal("the failure that reached the limit kept the key")
	}
	if !moved(func(pr SessionPR) (bool, error) { return r.RecordPRRead(pr, pr.Wakes, at) }) {
		t.Fatal("the read that cleared the failures kept the key")
	}
}
