package registry

import (
	"errors"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestARangedBodyReaderSeesASameLengthSamePrefixRewrite(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "version", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	retention := SessionJournalRetention{Count: 100, Bytes: 1 << 20}
	write := func(body string) {
		t.Helper()
		entry := SessionJournalEntry{SessionID: session.ID, Kind: "text", ProviderKey: "tool", Payload: []byte(`{"body":"` + body[:4] + `"}`), BodyRef: "ref", FullBody: []byte(body)}
		if _, _, err := r.AppendSessionJournalEntry(entry, retention); err != nil {
			t.Fatal(err)
		}
	}
	prefix := strings.Repeat("p", 32)
	write(prefix + "first")
	head, err := r.SessionJournalBodyRange(session.ID, "ref", 0, len(prefix), nil)
	if err != nil || string(head.Body) != prefix {
		t.Fatalf("first range: %+v err=%v", head, err)
	}
	write(prefix + "other")
	rest, err := r.SessionJournalBodyRange(session.ID, "ref", len(prefix), len(prefix)+16, nil)
	if err != nil || string(rest.Body) != "other" || rest.Total != head.Total {
		t.Fatalf("second range: %+v err=%v", rest, err)
	}
	if rest.Version == head.Version {
		t.Fatalf("the rewrite kept version %d, so the reader would join %q to %q", head.Version, head.Body, rest.Body)
	}
	refused, err := r.SessionJournalBodyRange(session.ID, "ref", len(prefix), len(prefix)+16, &head.Version)
	if !errors.Is(err, ErrSessionJournalBodyRewritten) || refused.Version != rest.Version || refused.Body != nil {
		t.Fatalf("a read expecting the old version: %+v err=%v", refused, err)
	}

	if _, err := r.AppendSessionJournalBody(SessionJournalEntry{SessionID: session.ID, Kind: "text", ProviderKey: "tool", Payload: []byte(`{}`), BodyRef: "ref"}, rest.Total, []byte(" more"), retention); err != nil {
		t.Fatal(err)
	}
	grown, err := r.SessionJournalBodyRange(session.ID, "ref", len(prefix), len(prefix)+16, &rest.Version)
	if err != nil || string(grown.Body) != "other more" || grown.Version != rest.Version {
		t.Fatalf("a read after an append to the tail: %+v err=%v", grown, err)
	}

	write("short")
	if _, err := r.SessionJournalBodyRange(session.ID, "ref", len(prefix), len(prefix)+16, &rest.Version); !errors.Is(err, ErrSessionJournalBodyRewritten) {
		t.Fatalf("a read past the end of a body rewritten shorter: err=%v, want the rewrite", err)
	}
}
