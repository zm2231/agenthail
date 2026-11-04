package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type sessionSourceManager struct {
	registry *registry.Registry
	prompts  func(surface.Session, sessionJournalPayload)
	// hookCaptured says whether a ledger still holds a hook capture of the session's words that its journal has not checked.
	hookCaptured func(sessionID string) bool
	rederives    chan struct{}
	mu           sync.Mutex
	sources      map[string]*sessionSource
	running      sync.WaitGroup
	closed       bool
}

type sessionSource struct {
	seeded          chan struct{}
	done            chan struct{}
	seedErr         error
	manager         *sessionSourceManager
	session         surface.Session
	adapter         surface.Surface
	epoch           string
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	providerMu      sync.Mutex
	followers       map[uint64]chan struct{}
	nextID          uint64
	holders         map[string]int
	appendBodies    map[string]string
	appendCursors   map[string]uint64
	streamCursor    uint64
	streamCursorSet bool
	streamCursorErr error
	anonymous       uint64
	sourceVersion   uint64
	appendFailed    bool
}

func newSessionSourceManager(store *registry.Registry) *sessionSourceManager {
	return &sessionSourceManager{registry: store, sources: map[string]*sessionSource{}, rederives: make(chan struct{}, journalRederives)}
}

func (m *sessionSourceManager) subscribe(session *surface.Session, adapter surface.Surface) (sessionSourceSubscription, error) {
	return m.subscribeContext(context.Background(), session, adapter)
}

func (m *sessionSourceManager) subscribeContext(waitContext context.Context, session *surface.Session, adapter surface.Surface) (sessionSourceSubscription, error) {
	if session == nil || adapter == nil {
		return sessionSourceSubscription{}, fmt.Errorf("session source requires session and adapter")
	}
	var subscription sessionSourceSubscription
	source, err := m.acquire(waitContext, session, adapter, func(source *sessionSource) {
		subscription = source.subscribe("viewer")
	})
	if err != nil {
		return sessionSourceSubscription{}, err
	}
	select {
	case <-source.seeded:
	case <-time.After(sessionJournalSeedTimeout):
	case <-waitContext.Done():
	}
	return subscription, nil
}

func (m *sessionSourceManager) hold(session *surface.Session, adapter surface.Surface, holder string) (func(), error) {
	_, release, err := m.holdSource(context.Background(), session, adapter, holder)
	return release, err
}

func (m *sessionSourceManager) holdSource(ctx context.Context, session *surface.Session, adapter surface.Surface, holder string) (*sessionSource, func(), error) {
	if strings.TrimSpace(holder) == "" {
		return nil, nil, fmt.Errorf("session source holder is required")
	}
	source, err := m.acquire(ctx, session, adapter, func(source *sessionSource) {
		source.mu.Lock()
		source.holders[holder]++
		source.mu.Unlock()
	})
	if err != nil {
		return nil, nil, err
	}
	return source, func() { source.releaseHolder(holder) }, nil
}

// acquire attaches to the session's running source, starting one if there is
// none. A source that has been stopped stays registered until its run returns,
// and acquire waits for that, so one session's journal never has two writers.
func (m *sessionSourceManager) acquire(ctx context.Context, session *surface.Session, adapter surface.Surface, attach func(*sessionSource)) (*sessionSource, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrSessionSourceManagerClosed
		}
		source := m.sources[session.ID]
		if source != nil && source.ctx.Err() != nil {
			m.mu.Unlock()
			select {
			case <-source.done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		start := false
		if source == nil {
			epoch, err := m.registry.BeginSessionJournalSource(session.ID)
			if err != nil {
				m.mu.Unlock()
				return nil, err
			}
			sourceCtx, cancel := context.WithCancel(context.Background())
			source = &sessionSource{manager: m, session: *session, adapter: adapter, epoch: epoch, ctx: sourceCtx, cancel: cancel, seeded: make(chan struct{}), done: make(chan struct{}), followers: map[uint64]chan struct{}{}, holders: map[string]int{}, appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
			m.sources[session.ID] = source
			m.running.Add(1)
			start = true
		}
		attach(source)
		m.mu.Unlock()
		if start {
			go source.run()
		}
		return source, nil
	}
}

func (m *sessionSourceManager) shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.running.Wait()
		return
	}
	m.closed = true
	for id, source := range m.sources {
		source.cancel()
		delete(m.sources, id)
	}
	m.mu.Unlock()
	m.running.Wait()
}

var ErrSessionSourceManagerClosed = errors.New("session source manager is closed")
