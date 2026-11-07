package daemon

import (
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

// fakeGoal is a test surface's goal. The daemon reads it on its own
// goroutines while a test changes it, so each change replaces the value
// under the lock and a read never sees one being written.
type fakeGoal struct {
	mu    sync.Mutex
	goal  *surface.GoalState
	err   error
	onGet func()
}

func (g *fakeGoal) setGoal(goal *surface.GoalState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.goal = goal
}

func (g *fakeGoal) setGoalErr(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

func (g *fakeGoal) setOnGoal(onGet func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onGet = onGet
}

func (g *fakeGoal) read() (*surface.GoalState, error) {
	g.mu.Lock()
	goal, err, onGet := g.goal, g.err, g.onGet
	g.mu.Unlock()
	if onGet != nil {
		onGet()
	}
	return goal, err
}

func (g *fakeGoal) changeGoal(change func(*surface.GoalState)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	next := *g.goal
	change(&next)
	g.goal = &next
}
