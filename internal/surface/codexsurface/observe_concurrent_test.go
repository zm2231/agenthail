package codexsurface

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestConcurrentObservationsOfDifferentThreadsEachReadTheirOwnThread(t *testing.T) {
	daemon := codextest.Start(t, nil)
	threads := make([]string, 8)
	for index := range threads {
		threads[index] = fmt.Sprintf("thread-%d", index)
		daemon.AddThread(codextest.Thread{ID: threads[index], Turns: []codextest.Turn{{ID: "turn-" + threads[index], Status: "completed", User: "ask", Assistant: "reply from " + threads[index]}}})
		if index%2 == 0 {
			daemon.Load(threads[index])
		}
	}
	codex := isolatedManagedRuntime(t)
	var observers sync.WaitGroup
	errs := make(chan error, len(threads)*4)
	for round := range 4 {
		for _, thread := range threads {
			observers.Add(1)
			go func() {
				defer observers.Done()
				observation, err := codex.Observe(context.Background(), &surface.Session{ID: thread, Surface: surface.KindCodex})
				switch {
				case err != nil:
					errs <- fmt.Errorf("round %d %s: %w", round, thread, err)
				case observation.CompletedTurnID != "turn-"+thread || observation.Reply == nil || observation.Reply.Text != "reply from "+thread:
					errs <- fmt.Errorf("round %d %s read completed=%q reply=%+v", round, thread, observation.CompletedTurnID, observation.Reply)
				}
			}()
		}
	}
	observers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestConcurrentRequestsOnOneAppServerConnectionEachGetTheirOwnReply(t *testing.T) {
	daemon := codextest.Start(t, nil)
	threads := make([]string, 8)
	for index := range threads {
		threads[index] = fmt.Sprintf("thread-%d", index)
		daemon.AddThread(codextest.Thread{ID: threads[index], Turns: []codextest.Turn{{ID: "turn-" + threads[index], Status: "completed", User: "ask", Assistant: "reply"}}})
	}
	client, err := isolatedManagedRuntime(t).home.dialManaged(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var requests sync.WaitGroup
	errs := make(chan error, len(threads)*8)
	for range 8 {
		for _, thread := range threads {
			requests.Add(1)
			go func() {
				defer requests.Done()
				response, err := client.Request(context.Background(), "thread/read", map[string]any{"threadId": thread, "includeTurns": false}, 5*time.Second)
				if err != nil {
					errs <- err
					return
				}
				result, _ := response["result"].(map[string]any)
				read, _ := result["thread"].(map[string]any)
				if read["id"] != thread {
					errs <- fmt.Errorf("a read of %s returned %v", thread, read["id"])
				}
			}()
		}
	}
	requests.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
