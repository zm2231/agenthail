package surface

import (
	"context"
	"sync"
	"time"
)

// A folder listing waits on the host, which kills its own listing at 15s.
const commandsListTimeout = 20 * time.Second

// A list runs off the stream's loop so a slow one never holds a timeline
// event back.
type CommandsWatch struct {
	refresh chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
}

func WatchCommands(ctx context.Context, list func(context.Context) (*RuntimeCommands, error), onEvent func(StreamEvent)) *CommandsWatch {
	ctx, cancel := context.WithCancel(ctx)
	w := &CommandsWatch{refresh: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	w.refresh <- struct{}{}
	go func() {
		defer close(w.done)
		var sent *RuntimeCommands
		var version uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.refresh:
			}
			listCtx, stop := context.WithTimeout(ctx, commandsListTimeout)
			commands, err := list(listCtx)
			stop()
			if err != nil || commands == nil || ctx.Err() != nil || SameRuntimeCommands(sent, commands) {
				continue
			}
			sent = commands
			version++
			onEvent(StreamEvent{ID: "commands", ProviderKey: "commands", Version: version, Operation: "replace", Kind: "commands", Commands: commands})
		}
	}()
	return w
}

func (w *CommandsWatch) Refresh() {
	if w == nil {
		return
	}
	select {
	case w.refresh <- struct{}{}:
	default:
	}
}

func (w *CommandsWatch) Stop() {
	if w == nil {
		return
	}
	w.cancel()
	<-w.done
}

func SerializedEvents(onEvent func(StreamEvent)) func(StreamEvent) {
	var mu sync.Mutex
	return func(event StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		onEvent(event)
	}
}

func SameRuntimeCommands(a, b *RuntimeCommands) bool {
	if a == nil || b == nil || a.Unavailable != b.Unavailable || len(a.Commands) != len(b.Commands) {
		return false
	}
	for i := range a.Commands {
		if a.Commands[i] != b.Commands[i] {
			return false
		}
	}
	return true
}
