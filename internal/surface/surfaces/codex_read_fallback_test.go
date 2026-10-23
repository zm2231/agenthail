package surfaces

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

// A managed session whose app-server read fails still opens: the page is the
// local transcript, exactly as it reads without the app-server, and says why.
func TestAManagedReadTheAppServerFailsShowsTheLocalTranscript(t *testing.T) {
	path := timelineFixture(t, codexTurnRecords+codexTurnRest)
	local, err := (&Codex{}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportManaged, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(local.Exchanges) == 0 || local.Warning != "" {
		t.Fatalf("local read=%+v err=%v", local, err)
	}
	for _, test := range []struct {
		name    string
		respond func(release <-chan struct{}) codextest.Responder
		timeout time.Duration
		warning string
	}{
		{"an RPC error", func(<-chan struct{}) codextest.Responder { return failingRead }, 5 * time.Second, "Codex Desktop native session read failed. Showing the local transcript instead."},
		{"no answer in time", func(release <-chan struct{}) codextest.Responder {
			return func(method string, _ map[string]any) map[string]any {
				if method == "thread/read" || method == "thread/turns/list" {
					<-release
				}
				return nil
			}
		}, 300 * time.Millisecond, "Codex Desktop did not answer the native session read in time. Showing the local transcript instead."},
	} {
		t.Run(test.name, func(t *testing.T) {
			release := make(chan struct{})
			startManagedCodex(t, test.respond(release))
			t.Cleanup(func() { close(release) })
			session := managedSession()
			session.Transcript = path
			session.Status = surface.StatusIdle
			ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
			defer cancel()
			read, err := isolatedManagedRuntime(t).ReadSession(ctx, session, surface.SessionReadRequest{Limit: 5})
			if err != nil {
				t.Fatal(err)
			}
			if read.Source == "rpc" || read.Warning != test.warning || read.UnavailableReason != "" {
				t.Fatalf("source=%q warning=%q unavailable=%q", read.Source, read.Warning, read.UnavailableReason)
			}
			if !reflect.DeepEqual(read.Items, local.Items) || !reflect.DeepEqual(read.Exchanges, local.Exchanges) || read.NextBefore != local.NextBefore {
				t.Fatalf("fallback page differs from the local read:\n%+v\n%+v", read, local)
			}
		})
	}
}

// When the local transcript cannot be read either, the app-server failure is
// added to the reason the page is unavailable rather than offered as a warning.
func TestAManagedReadWithNoTranscriptNamesBothFailures(t *testing.T) {
	startManagedCodex(t, failingRead)
	session := managedSession()
	read, err := isolatedManagedRuntime(t).ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if read.Warning != "" || !strings.HasSuffix(read.UnavailableReason, " Codex Desktop native session read failed.") || len(read.UnavailableReason) <= len(" Codex Desktop native session read failed.") {
		t.Fatalf("warning=%q unavailable=%q", read.Warning, read.UnavailableReason)
	}
}

func failingRead(method string, _ map[string]any) map[string]any {
	if method == "thread/read" || method == "thread/turns/list" {
		return map[string]any{"error": map[string]any{"code": -32603, "message": "synthetic read failure"}}
	}
	return nil
}
