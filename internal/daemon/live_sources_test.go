package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestALiveSessionNothingWatchesKeepsItsJournalFollowingItsTranscript(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	d, r, _, _, _ := daemonFixture(t)
	t.Cleanup(d.sources.shutdown)
	fixture := claudetest.New(t)
	const conversation = "live-unwatched"
	path := fixture.Transcript(conversation, fixture.Home)
	fixture.Listen()
	fixture.Record(conversation, fixture.Home, "")
	d.Surfaces = []surface.Surface{surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)}
	d.discoverCatalog(t.Context())
	if _, err := r.Session(conversation); err != nil {
		t.Fatalf("discovery did not list the live session: %v", err)
	}
	before := journalTop(t, d, conversation, func(seq uint64) bool { return seq > 0 })
	appendToFile(t, path, claudeTurns(conversation, 1, 1))
	deadline := time.Now().Add(time.Second)
	for journalTop(t, d, conversation, nil) <= before {
		if time.Now().After(deadline) {
			t.Fatalf("journal stayed at seq %d a second after its live session's transcript grew", before)
		}
		time.Sleep(10 * time.Millisecond)
	}

	fixture.RemoveRecord()
	d.discoverCatalog(t.Context())
	deadline = time.Now().Add(5 * time.Second)
	for {
		d.sources.mu.Lock()
		_, running := d.sources.sources[conversation]
		d.sources.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the source still runs after its session's process ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// journalTop returns the newest journal seq of a session, waiting up to five
// seconds for ready when it is set.
func journalTop(t *testing.T, d *Daemon, sessionID string, ready func(uint64) bool) uint64 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		page, err := d.readJournalPage(sessionID, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if ready == nil || ready(page.JournalSeq) {
			return page.JournalSeq
		}
		if time.Now().After(deadline) {
			t.Fatalf("journal of %s stayed at seq %d", sessionID, page.JournalSeq)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
