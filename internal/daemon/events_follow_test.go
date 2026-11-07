package daemon

import "testing"

// followEvents delivers the hub's events published after now, in order,
// until the test ends.
func followEvents(t testing.TB, hub *eventHub) <-chan apiEvent {
	t.Helper()
	_, follower, _ := hub.subscribe(hub.cursor())
	out := make(chan apiEvent, 1024)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range follower.Wake {
			events, _ := follower.read()
			for _, event := range events {
				select {
				case out <- event:
				case <-stop:
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		follower.Cancel()
		<-done
	})
	return out
}
