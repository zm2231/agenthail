package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestTheHostWatcherAsksForACatalogPassWhenTheTurnAFinishedTaskOwesStarts(t *testing.T) {
	release := filepath.Join(t.TempDir(), "notify")
	t.Setenv("FAKE_CLAUDE_NOTIFY_RELEASE", release)
	fixture := claudehosttest.New(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := fixture.Manager(t, "1.0.0").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	fixture.Open(t, client, id)
	d := newTestDaemon(t, hostRegistry(t), nil)
	d.log = log.New(io.Discard, "", 0)
	d.SetClaudeHost(fixture.Manager(t, "1.0.0"))
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		d.watchClaudeHostRequests(ctx)
	}()
	defer func() {
		cancel()
		<-watched
	}()
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: id, UUID: uuid.NewString(), Text: "BACKGROUND 200"}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the task to end and owe its turn", 15*time.Second, func() bool {
		listing, err := client.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, listed := range listing.Sessions {
			if listed.SessionID == id {
				return listed.State == "idle" && len(listed.Tasks) == 0 && listed.TaskTurnOwed
			}
		}
		return false
	})
	for quiet := false; !quiet; {
		select {
		case <-d.catalogNudge:
		case <-time.After(300 * time.Millisecond):
			quiet = true
		}
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-d.catalogNudge:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn a finished task owed started and asked for no catalog pass, so the session stays finishing")
	}
}
