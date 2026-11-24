package daemon

import (
	"context"
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

// A scan observes each watched session in a job of its own, so a slow
// provider call holds up only its session. Each surface runs at most
// observeJobsPerSurface jobs and the daemon at most observeJobs, taken from
// the surfaces in turn so one surface's slow sessions do not hold another's.
const (
	observeJobsPerSurface = 4
	observeJobs           = 8
)

type observeJob struct {
	ctx     context.Context
	session string
	run     func(context.Context)
	done    func()
}

// observeJobQueue keeps one job per session, queued or running: a session
// with a job is not given another until it finishes, so its queue drains in
// one place and the jobs waiting never outnumber the watched sessions.
type observeJobQueue struct {
	mu       sync.Mutex
	pending  map[string]struct{}
	queues   map[surface.SurfaceKind][]observeJob
	surfaces []surface.SurfaceKind
	next     int
	running  map[surface.SurfaceKind]int
	total    int
	workers  sync.WaitGroup
}

func newObserveJobQueue() *observeJobQueue {
	return &observeJobQueue{pending: map[string]struct{}{}, queues: map[surface.SurfaceKind][]observeJob{}, running: map[surface.SurfaceKind]int{}}
}

// add queues a job for the session unless it already has one, and reports
// whether it did.
func (q *observeJobQueue) add(kind surface.SurfaceKind, job observeJob) bool {
	q.mu.Lock()
	if _, found := q.pending[job.session]; found {
		q.mu.Unlock()
		return false
	}
	q.pending[job.session] = struct{}{}
	if _, known := q.queues[kind]; !known {
		q.surfaces = append(q.surfaces, kind)
	}
	q.queues[kind] = append(q.queues[kind], job)
	dropped := q.startLocked()
	q.mu.Unlock()
	finish(dropped)
	return true
}

func finish(jobs []observeJob) {
	for _, job := range jobs {
		job.done()
	}
}

// startLocked starts queued jobs up to the limits and returns the ones it
// dropped because their context ended, for the caller to finish unlocked.
func (q *observeJobQueue) startLocked() []observeJob {
	var dropped []observeJob
	for q.total < observeJobs {
		kind, job, found := q.takeLocked()
		if !found {
			break
		}
		if job.ctx.Err() != nil {
			delete(q.pending, job.session)
			dropped = append(dropped, job)
			continue
		}
		q.running[kind]++
		q.total++
		q.workers.Add(1)
		go q.work(kind, job)
	}
	return dropped
}

func (q *observeJobQueue) takeLocked() (surface.SurfaceKind, observeJob, bool) {
	for range q.surfaces {
		kind := q.surfaces[q.next%len(q.surfaces)]
		q.next = (q.next + 1) % len(q.surfaces)
		queue := q.queues[kind]
		if len(queue) == 0 || q.running[kind] >= observeJobsPerSurface {
			continue
		}
		job := queue[0]
		queue[0] = observeJob{}
		q.queues[kind] = queue[1:]
		return kind, job, true
	}
	return "", observeJob{}, false
}

func (q *observeJobQueue) work(kind surface.SurfaceKind, job observeJob) {
	defer q.workers.Done()
	defer func() {
		q.mu.Lock()
		q.running[kind]--
		q.total--
		delete(q.pending, job.session)
		dropped := q.startLocked()
		q.mu.Unlock()
		job.done()
		finish(dropped)
	}()
	job.run(job.ctx)
}

// wait returns once no job is running. Jobs still queued when their context
// ends are dropped without running.
func (q *observeJobQueue) wait() { q.workers.Wait() }

func (q *observeJobQueue) sessions() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}
