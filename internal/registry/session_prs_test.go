package registry

import (
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
