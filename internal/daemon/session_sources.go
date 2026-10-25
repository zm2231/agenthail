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
	mu       sync.Mutex
	sources  map[string]*sessionSource
	running  sync.WaitGroup
	closed   bool
}

type sessionSource struct {
	seeded          chan struct{}
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
	return &sessionSourceManager{registry: store, sources: map[string]*sessionSource{}}
}

func (m *sessionSourceManager) subscribe(session *surface.Session, adapter surface.Surface) (sessionSourceSubscription, error) {
	return m.subscribeContext(context.Background(), session, adapter)
}

func (m *sessionSourceManager) subscribeContext(waitContext context.Context, session *surface.Session, adapter surface.Surface) (sessionSourceSubscription, error) {
	if session == nil || adapter == nil {
		return sessionSourceSubscription{}, fmt.Errorf("session source requires session and adapter")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return sessionSourceSubscription{}, ErrSessionSourceManagerClosed
	}
	source := m.sources[session.ID]
	start := false
	if source == nil {
		epoch, err := m.registry.BeginSessionJournalSource(session.ID)
		if err != nil {
			m.mu.Unlock()
			return sessionSourceSubscription{}, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		source = &sessionSource{manager: m, session: *session, adapter: adapter, epoch: epoch, ctx: ctx, cancel: cancel, seeded: make(chan struct{}), followers: map[uint64]chan struct{}{}, holders: map[string]int{}, appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
		m.sources[session.ID] = source
		m.running.Add(1)
		start = true
	}
	subscription := source.subscribe("viewer")
	m.mu.Unlock()
	if start {
		go source.run()
	}
	select {
	case <-source.seeded:
	case <-time.After(sessionJournalSeedTimeout):
	case <-waitContext.Done():
	}
	return subscription, nil
}

func (m *sessionSourceManager) hold(session *surface.Session, adapter surface.Surface, holder string) (func(), error) {
	_, release, err := m.holdSource(session, adapter, holder)
	return release, err
}

func (m *sessionSourceManager) holdSource(session *surface.Session, adapter surface.Surface, holder string) (*sessionSource, func(), error) {
	if strings.TrimSpace(holder) == "" {
		return nil, nil, fmt.Errorf("session source holder is required")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, ErrSessionSourceManagerClosed
	}
	source := m.sources[session.ID]
	start := false
	if source == nil {
		epoch, err := m.registry.BeginSessionJournalSource(session.ID)
		if err != nil {
			m.mu.Unlock()
			return nil, nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		source = &sessionSource{manager: m, session: *session, adapter: adapter, epoch: epoch, ctx: ctx, cancel: cancel, seeded: make(chan struct{}), followers: map[uint64]chan struct{}{}, holders: map[string]int{}, appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
		m.sources[session.ID] = source
		m.running.Add(1)
		start = true
	}
	source.mu.Lock()
	source.holders[holder]++
	source.mu.Unlock()
	m.mu.Unlock()
	if start {
		go source.run()
	}
	return source, func() { source.releaseHolder(holder) }, nil
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
