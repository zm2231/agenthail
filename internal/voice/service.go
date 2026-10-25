package voice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/surface"
)

type Service struct {
	// mu guards the state and is never held across I/O.
	mu sync.Mutex
	// op keeps the actions that do I/O in arrival order.
	op             sync.Mutex
	applying       sync.WaitGroup
	call           context.Context
	cancelCall     context.CancelFunc
	saving         chan struct{}
	generation     uint64
	written        uint64
	unsaved        []byte
	spoken         []spokenRequest
	kept           map[string]bool // spoken transcripts the call held rather than delivered
	spokenInFlight string
	delivering     bool
	cycled         func()
	// interruptPoll ends the poll in flight, so a call that changes its
	// cursor is read at once rather than after the poll's wait.
	interruptPoll context.CancelFunc
	// effects is work queued under s.mu that runs, in order, once the lock is
	// released.
	effects                 []func()
	effectsWake             *sync.Cond
	effectsStopped          bool
	effectsDone             chan struct{}
	provider                Provider
	path                    string
	state                   diskState
	cursor                  Cursor
	lastSeen                atomic.Int64
	published               atomic.Pointer[snapshot]
	running                 bool
	register                func(context.Context, surface.Session) error
	loadErr                 error
	boundAttempt            string
	eventGap                bool
	disconnectedAt          time.Time
	commandPath             string
	target                  TargetResolver
	projects                Projects
	dispatcher              Deliverer
	setOperatorSourceActive func(session *surface.Session, active bool)
	stream                  SessionStreamProvider
	operatorSourceActive    bool
	clock                   clock
	background              context.Context
	stop                    context.CancelFunc
	tasks                   sync.WaitGroup
	closed                  bool
	speechTimeout           time.Duration
	logf                    func(format string, args ...any)
	briefer                 Briefer
	follow                  *follower
	replies                 []queuedReply
	replying                bool
	// rewait marks a poll ended to recompute its wait, so it is not reported as failed.
	rewait     bool
	lastSpeech time.Time
	listening  bool
	connected  chan struct{}
}

func New(path string, provider Provider, register func(context.Context, surface.Session) error, commandPath string) *Service {
	return newService(path, provider, register, commandPath, nil, delivery.Dispatcher{}, nil, nil, systemClock)
}

// A session-bound call follows its session's journal, so targets come with a stream.
func NewWithTargetsAndOperatorSourceAndStream(path string, provider Provider, register func(context.Context, surface.Session) error, commandPath string, target TargetResolver, dispatcher Deliverer, setOperatorSourceActive func(session *surface.Session, active bool), stream SessionStreamProvider) *Service {
	return newService(path, provider, register, commandPath, target, dispatcher, setOperatorSourceActive, stream, systemClock)
}

func newService(path string, provider Provider, register func(context.Context, surface.Session) error, commandPath string, target TargetResolver, dispatcher Deliverer, setOperatorSourceActive func(session *surface.Session, active bool), stream SessionStreamProvider, clock clock) *Service {
	s := &Service{path: path, provider: provider, register: register, commandPath: commandPath, target: target, dispatcher: dispatcher, setOperatorSourceActive: setOperatorSourceActive, stream: stream, clock: clock, speechTimeout: delegatedSpeechTimeout, saving: make(chan struct{}, 1)}
	s.background, s.stop = context.WithCancel(context.Background())
	s.effectsWake, s.effectsDone = sync.NewCond(&s.mu), make(chan struct{})
	s.state.State = State{Protocol: 1, Phase: "idle", Events: []Event{}}
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) > 2<<20 {
			s.loadErr = errors.New("voice state exceeds its size limit")
		} else {
			s.loadErr = json.Unmarshal(data, &s.state)
		}
		if s.state.State.Protocol != 1 {
			s.loadErr = errors.New("unsupported voice state version")
		}
		if s.active() {
			s.state.State.Phase = "unknown"
			s.state.State.Message = "The host restarted. End the previous call before reconnecting; agent work is retained."
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		s.loadErr = err
	}
	s.syncOperatorSourceActive()
	s.published.Store(s.capture())
	go s.runEffects()
	return s
}

func (s *Service) syncOperatorSourceActive() {
	active := s.active()
	if active == s.operatorSourceActive {
		return
	}
	s.operatorSourceActive = active
	if s.state.State.Session == nil || s.setOperatorSourceActive == nil {
		return
	}
	session, hold := *s.state.State.Session, s.setOperatorSourceActive
	s.later(func() { hold(&session, active) })
}

// later queues work that must not run under s.mu, such as closing a
// connection or holding a session source. It runs after the lock is released,
// in the order it was queued. The caller holds s.mu.
func (s *Service) later(effect func()) {
	s.effects = append(s.effects, effect)
	s.effectsWake.Signal()
}

func (s *Service) runEffects() {
	defer close(s.effectsDone)
	s.mu.Lock()
	for {
		for len(s.effects) == 0 && !s.effectsStopped {
			s.effectsWake.Wait()
		}
		if len(s.effects) == 0 {
			s.mu.Unlock()
			return
		}
		effect := s.effects[0]
		s.effects = s.effects[1:]
		s.mu.Unlock()
		effect()
		s.mu.Lock()
	}
}

// release frees the event source of the call whose cursor this is. The
// caller holds s.mu.
func (s *Service) release(threadID string, cursor Cursor) {
	s.later(func() { s.provider.Release(threadID, cursor) })
}

// Close stops the service's background work. It cancels the service context,
// refuses later actions, waits for actions and service work in progress, so
// nothing writes the state file after it returns, and releases the call's
// event source.
func (s *Service) Close() {
	s.stop()
	s.mu.Lock()
	s.closed = true
	s.unlock()
	s.applying.Wait()
	s.tasks.Wait()
	s.mu.Lock()
	if s.provider != nil && s.state.State.Session != nil {
		s.release(s.state.State.Session.ID, s.cursor)
	}
	s.effectsStopped = true
	s.effectsWake.Signal()
	s.unlock()
	<-s.effectsDone
	_ = s.flush()
	s.saving <- struct{}{}
	<-s.saving
}

// spawn starts background work the service owns, unless it is closed. The
// caller holds s.mu.
func (s *Service) spawn(task func()) bool {
	if s.closed {
		return false
	}
	s.tasks.Add(1)
	go func() {
		defer s.tasks.Done()
		task()
	}()
	return true
}
