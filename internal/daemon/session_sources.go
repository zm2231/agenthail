package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	sessionJournalRetentionCount = 2048
	sessionJournalRetentionBytes = 8 << 20
	sessionStreamBodyBytes       = 16 << 10
	sessionJournalSeedTimeout    = 12 * time.Second
)

var (
	sessionSeedRetryDelay          = 2 * time.Second
	sessionSeedRetryMaxDelay       = time.Minute
	sessionSnapshotRefreshInterval = 5 * time.Second
	sessionPageHandoffGrace        = 5 * time.Second
	ErrSessionSourceManagerClosed  = errors.New("session source manager is closed")
	sessionTranscriptMoveCheck     = 2 * time.Second
	errTranscriptReplaced          = fmt.Errorf("transcript identity changed: %w", surface.ErrTranscriptUnavailable)
)

type sessionJournalPayload struct {
	Context          *surface.ContextUsage    `json:"context,omitempty"`
	Goal             *surface.GoalState       `json:"goal"`
	Commands         []surface.RuntimeCommand `json:"commands,omitempty"`
	Unavailable      string                   `json:"unavailable,omitempty"`
	Role             string                   `json:"role,omitempty"`
	Origin           string                   `json:"origin,omitempty"`
	Sender           string                   `json:"sender,omitempty"`
	Title            string                   `json:"title,omitempty"`
	Status           string                   `json:"status,omitempty"`
	ResetsAt         string                   `json:"resetsAt,omitempty"`
	ItemID           string                   `json:"itemId"`
	ProviderKey      string                   `json:"providerKey,omitempty"`
	Version          uint64                   `json:"version"`
	Op               string                   `json:"op"`
	Kind             string                   `json:"kind"`
	TurnID           string                   `json:"turnId,omitempty"`
	CallID           string                   `json:"callId,omitempty"`
	Final            bool                     `json:"final,omitempty"`
	TS               string                   `json:"ts"`
	Body             string                   `json:"body,omitempty"`
	Truncated        bool                     `json:"truncated"`
	TruncationReason string                   `json:"truncationReason,omitempty"`
	BodyRef          string                   `json:"bodyRef,omitempty"`
	Reason           string                   `json:"reason,omitempty"`
	Attachment       *surface.Attachment      `json:"attachment,omitempty"`
	Files            []surface.AttachedFile   `json:"files,omitempty"`
	Turn             *surface.TurnMetadata    `json:"turn,omitempty"`
}

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
	// appendFailed records that a journal append of this source failed. The
	// source then never records a checkpoint, since a later one would claim
	// the items it lost.
	appendFailed bool
}

// sessionSourceSubscription follows a session's journal. Wake receives a
// signal after the source journals rows and closes when the source ends; the
// follower reads the rows after its own cursor, so a burst it is slow to read
// waits in the journal instead of ending the subscription.
type sessionSourceSubscription struct {
	Wake   <-chan struct{}
	Cancel func()
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

func (s *sessionSource) subscribe(holder string) sessionSourceSubscription {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	wake := make(chan struct{}, 1)
	s.followers[id] = wake
	s.holders[holder]++
	s.mu.Unlock()
	return sessionSourceSubscription{Wake: wake, Cancel: func() {
		s.mu.Lock()
		if existing, found := s.followers[id]; found {
			delete(s.followers, id)
			close(existing)
		}
		s.holders[holder]--
		if s.holders[holder] <= 0 {
			delete(s.holders, holder)
		}
		stop := len(s.followers) == 0 && len(s.holders) == 0
		s.mu.Unlock()
		if stop {
			s.stop()
		}
	}}
}

func (s *sessionSource) releaseHolder(holder string) {
	s.mu.Lock()
	s.holders[holder]--
	if s.holders[holder] <= 0 {
		delete(s.holders, holder)
	}
	stop := len(s.followers) == 0 && len(s.holders) == 0
	s.mu.Unlock()
	if stop {
		s.stop()
	}
}

func (s *sessionSource) stop() {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.followers) != 0 || len(s.holders) != 0 {
		return
	}
	s.cancel()
	if s.manager.sources[s.session.ID] == s {
		delete(s.manager.sources, s.session.ID)
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

func (s *sessionSource) run() {
	provider, localTranscript := s.adapter.(surface.LocalTranscriptProvider)
	if !localTranscript || !provider.RequiresLocalTranscript(&s.session) {
		if reader, ok := s.adapter.(surface.StreamCursorReader); ok {
			if cursor, err := reader.StreamCursor(s.ctx, &s.session); err == nil {
				s.streamCursor = cursor
				s.streamCursorSet = true
			} else if !errors.Is(err, surface.ErrUnsupported) && s.ctx.Err() == nil {
				s.streamCursorErr = fmt.Errorf("session stream cursor unavailable: %w", err)
			}
		}
	}
	defer func() {
		s.closeFollowers()
		s.remove()
		s.manager.running.Done()
	}()
	seedErr := s.seedJournal()
	s.setSeedErr(seedErr)
	close(s.seeded)
	if s.streamCursorErr != nil {
		s.appendSourceError(s.streamCursorErr)
		<-s.ctx.Done()
		return
	}
	for delay := sessionSeedRetryDelay; seedErr != nil; delay = min(2*delay, sessionSeedRetryMaxDelay) {
		if !s.wait(delay) {
			return
		}
		seedErr = s.seedJournal()
		s.setSeedErr(seedErr)
	}
	if !surface.EffectiveCapabilities(s.adapter, &s.session).Stream {
		for s.wait(sessionSnapshotRefreshInterval) {
			s.setSeedErr(s.seedJournal())
		}
		return
	}
	followDelay := sessionSeedRetryDelay
	for {
		current := s.session
		if refreshed, refreshErr := s.manager.registry.Session(s.session.ID); refreshErr == nil {
			current = *refreshed
		}
		if err := s.followTranscript(current); err != nil {
			if s.ctx.Err() == nil {
				s.appendSourceError(err)
			}
			if !s.wait(followDelay) {
				break
			}
			followDelay = min(2*followDelay, sessionSeedRetryMaxDelay)
			continue
		}
		followDelay = sessionSeedRetryDelay
		current.StreamCursor = s.streamCursor
		current.StreamCursorSet = s.streamCursorSet
		current.TranscriptOffset = s.session.TranscriptOffset
		current.TranscriptOffsetSet = s.session.TranscriptOffsetSet
		current.TranscriptIdentity = s.session.TranscriptIdentity
		current.CodexPendingEventUser = s.session.CodexPendingEventUser
		current.CodexPendingEventTurn = s.session.CodexPendingEventTurn
		current.CodexCurrentTurnID = s.session.CodexCurrentTurnID
		current.ClaudeClosingTurn = s.session.ClaudeClosingTurn
		current.TranscriptFromStart = s.session.TranscriptFromStart
		streamContext, stopStream := context.WithCancel(s.ctx)
		var moved atomic.Bool
		watched := make(chan struct{})
		if _, ok := s.adapter.(surface.TranscriptContinuer); ok && current.Transcript != "" {
			go func() {
				defer close(watched)
				s.watchTranscriptMove(streamContext, current.Transcript, func() {
					moved.Store(true)
					stopStream()
				})
			}()
		} else {
			close(watched)
		}
		streamErr := s.adapter.Stream(streamContext, &current, "", s.append, 30*time.Minute)
		stopStream()
		<-watched
		if moved.Load() {
			streamErr = nil
		}
		if current.Transcript == s.session.Transcript && current.TranscriptIdentity != "" && (current.TranscriptIdentity == s.session.TranscriptIdentity || !s.session.TranscriptOffsetSet) && current.TranscriptOffsetSet && current.TranscriptOffset >= s.session.TranscriptOffset {
			s.session.TranscriptOffset = current.TranscriptOffset
			s.session.TranscriptOffsetSet = true
			s.session.TranscriptIdentity = current.TranscriptIdentity
			s.session.CodexPendingEventUser = current.CodexPendingEventUser
			s.session.CodexPendingEventTurn = current.CodexPendingEventTurn
			s.session.CodexCurrentTurnID = current.CodexCurrentTurnID
			s.session.ClaudeClosingTurn = current.ClaudeClosingTurn
			s.session.TranscriptFromStart = false
			if err := s.recordTranscriptCheckpoint(s.session.TranscriptOffset, s.session.TranscriptIdentity); err != nil {
				s.appendSourceError(fmt.Errorf("record the transcript checkpoint: %w", err))
			}
		}
		if errors.Is(streamErr, surface.ErrUnsupported) {
			<-s.ctx.Done()
			break
		}
		if streamErr != nil && !errors.Is(streamErr, surface.ErrStreamWindow) && s.ctx.Err() == nil {
			s.appendSourceError(streamErr)
		}
		s.mu.Lock()
		hasHolders := len(s.holders) > 0
		s.mu.Unlock()
		if s.ctx.Err() != nil || !hasHolders {
			break
		}
		select {
		case <-s.ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

// followTranscript moves the source to the transcript the registry now
// names when it continues the one the source was reading.
func (s *sessionSource) followTranscript(current surface.Session) error {
	if _, ok := s.adapter.(surface.TranscriptContinuer); !ok || current.Transcript == "" || s.session.Transcript == "" || current.Transcript == s.session.Transcript {
		return nil
	}
	previous := s.session
	s.session.Transcript = current.Transcript
	s.session.TranscriptOffset = 0
	s.session.TranscriptOffsetSet = false
	ctx, cancel := context.WithTimeout(s.ctx, sessionJournalSeedTimeout)
	defer cancel()
	if err := s.catchUpLocalTranscript(ctx, previous.TranscriptIdentity, previous.Transcript); err != nil {
		s.session = previous
		return err
	}
	return nil
}

func (s *sessionSource) watchTranscriptMove(ctx context.Context, transcript string, moved func()) {
	ticker := time.NewTicker(sessionTranscriptMoveCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if refreshed, err := s.manager.registry.Session(s.session.ID); err == nil && refreshed.Transcript != "" && refreshed.Transcript != transcript {
				moved()
				return
			}
		}
	}
}

func (s *sessionSource) closeFollowers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, follower := range s.followers {
		delete(s.followers, id)
		close(follower)
	}
}

func (s *sessionSource) remove() {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	if s.manager.sources[s.session.ID] == s {
		delete(s.manager.sources, s.session.ID)
	}
}

// servesPageAtOnce reports whether a session's newest page can be read from
// its journal before its source's first pass: the session streams, its journal
// was seeded before, and that pass only appends to it. A journal written by an
// earlier item derivation is rebuilt by the pass instead, which drops rows a
// client already shown them would keep.
func (m *sessionSourceManager) servesPageAtOnce(session *surface.Session, adapter surface.Surface) (bool, error) {
	if !surface.EffectiveCapabilities(adapter, session).Stream {
		return false, nil
	}
	status, seedSeq, _, err := m.registry.SessionJournalSeedCheckpoint(session.ID)
	if err != nil || status != registry.SessionJournalSeeded || seedSeq == 0 {
		return false, err
	}
	if _, ok := adapter.(surface.TranscriptItemReader); ok {
		version, err := m.registry.SessionJournalDerivation(session.ID)
		if err != nil || version < transcriptJournalDerivation {
			return false, err
		}
	}
	return true, nil
}

// start runs the session's source, holding it for the page handoff grace,
// without waiting for its seed.
func (m *sessionSourceManager) start(session *surface.Session, adapter surface.Surface) error {
	_, release, err := m.holdSource(session, adapter, "page-seed")
	if err != nil {
		return err
	}
	time.AfterFunc(sessionPageHandoffGrace, release)
	return nil
}

func (m *sessionSourceManager) seed(ctx context.Context, session *surface.Session, adapter surface.Surface) error {
	source, release, err := m.holdSource(session, adapter, "page-seed")
	if err != nil {
		return err
	}
	defer func() { time.AfterFunc(sessionPageHandoffGrace, release) }()
	select {
	case <-source.seeded:
		return source.seedError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *sessionSourceManager) readHistory(ctx context.Context, session *surface.Session, adapter surface.Surface, before int64, limit int) (*surface.SessionReadResult, error) {
	source, release, err := m.holdSource(session, adapter, "history-read")
	if err != nil {
		return nil, err
	}
	defer func() { time.AfterFunc(sessionPageHandoffGrace, release) }()
	select {
	case <-source.seeded:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	source.providerMu.Lock()
	defer source.providerMu.Unlock()
	request := *session
	read, err := surface.ReadSession(ctx, adapter, &request, surface.SessionReadRequest{Before: before, Limit: limit})
	if err != nil {
		return nil, err
	}
	if read == nil {
		return nil, fmt.Errorf("session source returned no older activity")
	}
	return read, nil
}

func (s *sessionSource) appendSourceError(streamErr error) {
	_ = s.manager.registry.MarkSessionJournalSeed(s.session.ID, false)
	s.mu.Lock()
	s.sourceVersion++
	payload := sessionJournalPayload{
		ItemID:      s.epoch + ":source",
		ProviderKey: s.epoch + ":source",
		Version:     s.sourceVersion,
		Op:          "reset",
		Kind:        "source-error",
		TS:          time.Now().UTC().Format(time.RFC3339Nano),
		Reason:      boundedSessionSourceReason(streamErr.Error()),
	}
	s.mu.Unlock()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, changed, err := s.manager.registry.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.session.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
	if err != nil {
		return
	}
	if changed {
		s.wakeFollowers()
	}
}

func boundedSessionSourceReason(value string) string {
	const limit = 240
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "The session source stopped unexpectedly."
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func (s *sessionSource) seedJournal() error {
	err := s.loadSeed()
	if err != nil {
		s.appendSourceError(err)
	}
	return err
}

func (s *sessionSource) loadSeed() error {
	seedStatus, seedSeq, trustedIdentity, statusErr := s.manager.registry.SessionJournalSeedCheckpoint(s.session.ID)
	localTranscript := false
	if provider, ok := s.adapter.(surface.LocalTranscriptProvider); ok {
		localTranscript = provider.RequiresLocalTranscript(&s.session)
	}
	// Turns written while no source ran exist only in the transcript.
	transcriptBacked := localTranscript || s.session.Surface == surface.KindClaude
	streamable := surface.EffectiveCapabilities(s.adapter, &s.session).Stream
	seeded := statusErr == nil && seedStatus == registry.SessionJournalSeeded
	if seeded && !transcriptBacked && streamable {
		return nil
	}
	if localTranscript && trustedIdentity == "" && seedSeq > 0 {
		return fmt.Errorf("Codex local transcript identity checkpoint is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	ctx, cancel := context.WithTimeout(s.ctx, sessionJournalSeedTimeout)
	defer cancel()
	if seeded && transcriptBacked {
		caughtUp, err := s.catchUpFromCheckpoint(ctx, trustedIdentity)
		if err == nil && !caughtUp {
			err = s.catchUpLocalTranscript(ctx, trustedIdentity, "")
		}
		if err == nil {
			err = s.rederiveJournal(ctx)
		}
		if localTranscript || !errors.Is(err, errTranscriptReplaced) {
			return err
		}
		// A replaced Claude transcript seeds again from the new file.
		trustedIdentity = ""
	}
	s.providerMu.Lock()
	read, err := surface.ReadSession(ctx, s.adapter, &s.session, surface.SessionReadRequest{Limit: 40})
	s.providerMu.Unlock()
	if err != nil || read == nil {
		if err == nil {
			err = fmt.Errorf("session source returned no activity result")
		}
		return err
	}
	if trustedIdentity != "" && read.TranscriptIdentity != trustedIdentity {
		if _, continues := s.adapter.(surface.TranscriptContinuer); continues {
			return s.catchUpLocalTranscript(ctx, trustedIdentity, "")
		}
		return fmt.Errorf("Codex local transcript was replaced before catch-up: %w", errTranscriptReplaced)
	}
	if read.TranscriptOffsetSet {
		s.session.TranscriptOffset = read.TranscriptOffset
		s.session.TranscriptOffsetSet = true
		s.session.CodexPendingEventUser = read.CodexPendingEventUser
		s.session.CodexPendingEventTurn = read.CodexPendingEventTurn
		s.session.CodexCurrentTurnID = read.CodexCurrentTurnID
		s.session.ClaudeClosingTurn = read.ClaudeClosingTurn
		s.session.TranscriptIdentity = read.TranscriptIdentity
	} else if localTranscript {
		return fmt.Errorf("Codex local transcript is unavailable")
	}
	s.session.TranscriptFromStart = !read.TranscriptOffsetSet
	if localTranscript && read.TranscriptIdentity == "" {
		return fmt.Errorf("Codex local transcript identity is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	if err := s.manager.registry.RecordSessionJournalHistoryBoundary(s.session.ID, max(0, read.NextBefore)); err != nil {
		return err
	}
	s.appendSeedItems(read.Items)
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, read.TranscriptIdentity); err != nil {
		return err
	}
	if _, ok := s.adapter.(surface.TranscriptItemReader); ok {
		if err := s.recordTranscriptCheckpoint(read.TranscriptOffset, read.TranscriptIdentity); err != nil {
			return err
		}
		return s.manager.registry.MarkSessionJournalDerivation(s.session.ID, transcriptJournalDerivation)
	}
	return nil
}

// forwardCatchUpLimit bounds how much transcript a source reads forward from
// its checkpoint. A longer backlog is caught up by paging back from the
// transcript's end, which stops at the journal.
var forwardCatchUpLimit int64 = 32 << 20

// catchUpFromCheckpoint appends the items the transcript recorded after the
// journal's checkpoint, reading each record once. It reports false, having
// changed nothing, when there is no checkpoint, the transcript was replaced,
// or the backlog is past forwardCatchUpLimit.
func (s *sessionSource) catchUpFromCheckpoint(ctx context.Context, trustedIdentity string) (bool, error) {
	reader, ok := s.adapter.(surface.TranscriptItemReader)
	if !ok {
		return false, nil
	}
	checkpoint, found, err := s.manager.registry.SessionTranscriptCheckpoint(s.session.ID)
	if err != nil || !found || (trustedIdentity != "" && checkpoint.Identity != trustedIdentity) {
		return false, err
	}
	s.providerMu.Lock()
	read, err := reader.TranscriptItems(ctx, &s.session, surface.TranscriptItemsRequest{From: checkpoint.Offset, Identity: checkpoint.Identity, MaxBytes: forwardCatchUpLimit})
	s.providerMu.Unlock()
	if errors.Is(err, surface.ErrTranscriptChanged) || errors.Is(err, surface.ErrTranscriptBacklog) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !s.session.TranscriptOffsetSet {
		s.session.TranscriptOffset = read.End
		s.session.TranscriptOffsetSet = true
		s.session.TranscriptIdentity = read.Identity
		s.session.ClaudeClosingTurn = read.ClaudeClosingTurn
	}
	s.appendSeedItems(read.Items)
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, read.Identity); err != nil {
		return false, err
	}
	return true, s.recordTranscriptCheckpoint(read.End, read.Identity)
}

// recordTranscriptCheckpoint records that the journal holds the transcript's
// items before offset. Once an append of this source failed it clears the
// checkpoint instead, so the next source pages back to the journal.
func (s *sessionSource) recordTranscriptCheckpoint(offset int64, identity string) error {
	if _, ok := s.adapter.(surface.TranscriptItemReader); !ok || identity == "" {
		return nil
	}
	s.mu.Lock()
	failed := s.appendFailed
	s.mu.Unlock()
	if failed {
		return s.manager.registry.ClearSessionTranscriptCheckpoint(s.session.ID)
	}
	return s.manager.registry.SetSessionTranscriptCheckpoint(s.session.ID, registry.TranscriptCheckpoint{Offset: offset, Identity: identity})
}

func (s *sessionSource) setSeedErr(err error) {
	s.mu.Lock()
	s.seedErr = err
	s.mu.Unlock()
}

func (s *sessionSource) seedError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seedErr
}

func (s *sessionSource) wait(delay time.Duration) bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

// catchUpLocalTranscript journals what the transcript gained since the
// journal last read it, trustedIdentity at trustedPath when the caller knows
// the path. A transcript that moved to a file continuing it is followed, and
// the items the move dropped are removed.
func (s *sessionSource) catchUpLocalTranscript(ctx context.Context, trustedIdentity, trustedPath string) error {
	known, err := s.knownJournalItems()
	if err != nil {
		return fmt.Errorf("read journal seed identities: %w", err)
	}
	pages := make([][]surface.TimelineItem, 0, 2)
	before := int64(0)
	transcriptIdentity := trustedIdentity
	var end int64
	var dropped []surface.TimelineItem
	continued := false
	readToStart := false
	for {
		s.providerMu.Lock()
		read, err := surface.ReadSession(ctx, s.adapter, &s.session, surface.SessionReadRequest{Before: before, Limit: 40})
		s.providerMu.Unlock()
		if err != nil || read == nil {
			if err == nil {
				err = fmt.Errorf("session source returned no activity result")
			}
			return err
		}
		if !read.TranscriptOffsetSet {
			if len(pages) == 0 && trustedIdentity == "" && s.session.Surface == surface.KindClaude {
				// A seed that ran before the first turn, with nothing from a transcript journaled
				// since, means none was written: whatever is written next is new.
				held, err := s.journalHoldsTranscript()
				if err != nil {
					return err
				}
				if !held {
					s.session.TranscriptFromStart = true
					return nil
				}
			}
			return fmt.Errorf("%s transcript is unavailable", s.session.Surface)
		}
		if transcriptIdentity == "" {
			transcriptIdentity = read.TranscriptIdentity
		} else if read.TranscriptIdentity != transcriptIdentity {
			continuer, ok := s.adapter.(surface.TranscriptContinuer)
			if !ok || len(pages) > 0 {
				phase := "during catch-up"
				if len(pages) == 0 {
					phase = "before catch-up"
				}
				return fmt.Errorf("%s transcript was replaced %s: %w", s.session.Surface, phase, errTranscriptReplaced)
			}
			s.providerMu.Lock()
			dropped, continued, err = continuer.TranscriptContinuation(ctx, &s.session, trustedPath, transcriptIdentity)
			s.providerMu.Unlock()
			if err != nil {
				return err
			}
			if !continued {
				return s.restartJournal(read)
			}
			transcriptIdentity = read.TranscriptIdentity
		}
		if read.TranscriptOffsetSet && !s.session.TranscriptOffsetSet {
			s.session.TranscriptOffset = read.TranscriptOffset
			s.session.TranscriptOffsetSet = true
			s.session.CodexPendingEventUser = read.CodexPendingEventUser
			s.session.CodexPendingEventTurn = read.CodexPendingEventTurn
			s.session.CodexCurrentTurnID = read.CodexCurrentTurnID
			s.session.ClaudeClosingTurn = read.ClaudeClosingTurn
			s.session.TranscriptIdentity = read.TranscriptIdentity
		}
		if len(pages) == 0 {
			end = read.TranscriptOffset
		}
		pages = append(pages, read.Items)
		if timelineItemsOverlapJournal(read.Items, known) {
			break
		}
		if read.NextBefore == 0 {
			readToStart = true
			break
		}
		before = read.NextBefore
	}
	for index := len(pages) - 1; index >= 0; index-- {
		s.appendSeedItems(pages[index])
	}
	if continued {
		s.removeDroppedItems(dropped, known)
	}
	// Cursors into a continued transcript address its whole history, so a
	// boundary recorded against the file it continues no longer holds once
	// the journal has read that history back to its start.
	if continued && readToStart {
		if err := s.manager.registry.LowerSessionJournalHistoryBoundary(s.session.ID, 0); err != nil {
			return err
		}
	}
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, transcriptIdentity); err != nil {
		return err
	}
	return s.recordTranscriptCheckpoint(end, transcriptIdentity)
}

// restartJournal rebuilds the journal from a fresh read of a transcript that
// replaced the one the journal was read from but cannot be shown to continue
// it, with a notice saying so. Rows the fresh read does not hold are removed.
func (s *sessionSource) restartJournal(read *surface.SessionReadResult) error {
	notice := surface.TimelineItem{
		ID:        "transcript-restart:" + read.TranscriptIdentity,
		Kind:      "notice",
		Title:     "Conversation reloaded",
		Text:      "This conversation moved to a new transcript that could not be matched to the one shown before, so it was read again from the new transcript.",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	var fresh []surface.StreamEvent
	keep := map[string]bool{}
	for _, item := range append(slices.Clone(read.Items), notice) {
		if item.ID == "" {
			continue
		}
		event := seedEvent(s.session.Surface, item)
		keep[event.ProviderKey] = true
		fresh = append(fresh, event)
	}
	var events []surface.StreamEvent
	for before := uint64(0); ; {
		page, err := s.manager.registry.ReadSessionJournalPage(s.session.ID, before, 200)
		var gap *registry.SessionJournalHistoryGapError
		if errors.As(err, &gap) {
			break
		}
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			var payload sessionJournalPayload
			if json.Unmarshal(entry.Payload, &payload) != nil || payload.Op == "remove" || payload.Kind == "source-error" || payload.ProviderKey == "" || keep[payload.ProviderKey] {
				continue
			}
			keep[payload.ProviderKey] = true
			events = append(events, surface.StreamEvent{ID: payload.ItemID, ProviderKey: payload.ProviderKey, Operation: "remove", Kind: payload.Kind, TurnID: payload.TurnID, Final: true})
		}
		if page.NextBefore == 0 {
			break
		}
		before = page.NextBefore
	}
	events = append(events, fresh...)
	entries := make([]registry.SessionJournalEntry, 0, len(events))
	bodies := map[string]string{}
	for _, event := range events {
		s.mu.Lock()
		payload, bodyKey := s.normalizeLocked(event)
		s.mu.Unlock()
		entry, err := sessionJournalEntry(s.session.ID, payload)
		if err != nil {
			return err
		}
		if bodyKey != "" {
			bodies[bodyKey] = payload.Body
		}
		entries = append(entries, entry)
	}
	derivation, err := s.manager.registry.SessionJournalDerivation(s.session.ID)
	if err != nil {
		return err
	}
	restarted, err := s.manager.registry.RestartSessionJournal(s.session.ID, entries, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}, derivation, max(0, read.NextBefore))
	if err != nil {
		return err
	}
	s.session.TranscriptOffset = read.TranscriptOffset
	s.session.TranscriptOffsetSet = read.TranscriptOffsetSet
	s.session.CodexPendingEventUser = read.CodexPendingEventUser
	s.session.CodexPendingEventTurn = read.CodexPendingEventTurn
	s.session.CodexCurrentTurnID = read.CodexCurrentTurnID
	s.session.ClaudeClosingTurn = read.ClaudeClosingTurn
	s.session.TranscriptIdentity = read.TranscriptIdentity
	s.mu.Lock()
	for key, body := range bodies {
		s.appendBodies[key] = body
	}
	s.mu.Unlock()
	if len(restarted) > 0 {
		s.wakeFollowers()
	}
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, read.TranscriptIdentity); err != nil {
		return err
	}
	return s.recordTranscriptCheckpoint(read.TranscriptOffset, read.TranscriptIdentity)
}

func (s *sessionSource) removeDroppedItems(items []surface.TimelineItem, known map[string]struct{}) {
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		event := seedEvent(s.session.Surface, item)
		if _, ok := known[event.ProviderKey]; !ok {
			continue
		}
		s.append(surface.StreamEvent{ID: event.ID, ProviderKey: event.ProviderKey, Operation: "remove", Kind: item.Kind, TurnID: item.TurnID, Final: true})
	}
}

// transcriptJournalDerivation versions how a transcript-backed journal's
// items are derived. A journal written by an earlier derivation is rebuilt
// from the transcript once, at a source start.
const transcriptJournalDerivation = 1

// rederiveJournal rebuilds a journal written by an earlier derivation from
// the transcript, from the oldest offset it retains, keeping each row the
// transcript does not hold after the item it followed. The journal records
// the derivation, so a rebuilt journal is never rebuilt again.
func (s *sessionSource) rederiveJournal(ctx context.Context) error {
	reader, ok := s.adapter.(surface.TranscriptItemReader)
	if !ok {
		return nil
	}
	version, err := s.manager.registry.SessionJournalDerivation(s.session.ID)
	if err != nil || version >= transcriptJournalDerivation {
		return err
	}
	var rows []registry.SessionJournalEntry
	before := uint64(0)
	for {
		page, err := s.manager.registry.ReadSessionJournalPage(s.session.ID, before, 200)
		var gap *registry.SessionJournalHistoryGapError
		if errors.As(err, &gap) {
			break
		}
		if err != nil {
			return err
		}
		rows = append(page.Entries, rows...)
		if page.NextBefore == 0 {
			break
		}
		before = page.NextBefore
	}
	from := int64(-1)
	for _, row := range rows {
		if offset, ok := transcriptItemOffset(transcriptRowItemID(row)); ok && (from < 0 || offset < from) {
			from = offset
		}
	}
	if from < 0 {
		return s.manager.registry.MarkSessionJournalDerivation(s.session.ID, transcriptJournalDerivation)
	}
	s.providerMu.Lock()
	read, err := reader.TranscriptItems(ctx, &s.session, surface.TranscriptItemsRequest{From: from})
	s.providerMu.Unlock()
	if err != nil {
		return err
	}
	items := read.Items
	derived := make(map[string]bool, len(items))
	for _, item := range items {
		derived[item.ID] = true
	}
	var leading []registry.SessionJournalEntry
	following := map[string][]registry.SessionJournalEntry{}
	last := ""
	for _, row := range rows {
		if id := transcriptRowItemID(row); id != "" {
			if derived[id] {
				last = id
			}
			continue
		}
		if row.BodyRef != "" {
			full, found, err := s.manager.registry.SessionJournalEntryByProviderKey(s.session.ID, row.ProviderKey)
			if err != nil {
				return err
			}
			if found {
				row.FullBody = full.FullBody
			}
		}
		if last == "" {
			leading = append(leading, row)
		} else {
			following[last] = append(following[last], row)
		}
	}
	entries := leading
	bodies := map[string]string{}
	for _, item := range items {
		s.mu.Lock()
		payload, bodyKey := s.normalizeLocked(seedEvent(s.session.Surface, item))
		s.mu.Unlock()
		entry, err := sessionJournalEntry(s.session.ID, payload)
		if err != nil {
			return err
		}
		if bodyKey != "" {
			bodies[bodyKey] = item.Text
		}
		entries = append(entries, entry)
		entries = append(entries, following[item.ID]...)
	}
	replaced, err := s.manager.registry.ReplaceSessionJournal(s.session.ID, entries, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}, transcriptJournalDerivation)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for key, body := range bodies {
		s.appendBodies[key] = body
	}
	s.mu.Unlock()
	if len(replaced) > 0 {
		s.wakeFollowers()
	}
	return nil
}

// transcriptRowItemID returns the transcript item a journal row records, or
// "" for a row the transcript does not hold.
func transcriptRowItemID(row registry.SessionJournalEntry) string {
	id, found := strings.CutPrefix(row.ProviderKey, "timeline:")
	if !found {
		return ""
	}
	return strings.TrimSuffix(id, ":phase:"+row.Kind)
}

// transcriptItemOffset reads the transcript offset a Claude timeline item ID
// begins with.
func transcriptItemOffset(itemID string) (int64, bool) {
	digits, _, found := strings.Cut(itemID, "-")
	if !found {
		return 0, false
	}
	offset, err := strconv.ParseInt(digits, 10, 64)
	return offset, err == nil && offset >= 0
}

// journalHoldsTranscript reports whether anything journaled for the session
// came from its transcript; a source's own errors are the only entries that
// do not.
func (s *sessionSource) journalHoldsTranscript() (bool, error) {
	before := uint64(0)
	for {
		page, err := s.manager.registry.ReadSessionJournalPage(s.session.ID, before, 200)
		if err != nil {
			var gap *registry.SessionJournalHistoryGapError
			if errors.As(err, &gap) {
				// Retention dropped older entries, so the journal once held more than errors.
				return true, nil
			}
			return false, err
		}
		for _, entry := range page.Entries {
			if entry.Kind != "source-error" {
				return true, nil
			}
		}
		if page.NextBefore == 0 {
			return false, nil
		}
		before = page.NextBefore
	}
}

func (s *sessionSource) knownJournalItems() (map[string]struct{}, error) {
	known := map[string]struct{}{}
	before := uint64(0)
	for {
		page, err := s.manager.registry.ReadSessionJournalPage(s.session.ID, before, 200)
		if err != nil {
			var gap *registry.SessionJournalHistoryGapError
			if errors.As(err, &gap) {
				return known, nil
			}
			return nil, err
		}
		for _, entry := range page.Entries {
			var payload sessionJournalPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				continue
			}
			if payload.ItemID != "" {
				known[payload.ItemID] = struct{}{}
			}
			if payload.ProviderKey != "" {
				known[payload.ProviderKey] = struct{}{}
			}
		}
		if page.NextBefore == 0 {
			return known, nil
		}
		before = page.NextBefore
	}
}

func timelineItemsOverlapJournal(items []surface.TimelineItem, known map[string]struct{}) bool {
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if _, ok := known[item.ID]; ok {
			return true
		}
		if _, ok := known["timeline:"+item.ID]; ok {
			return true
		}
	}
	return false
}

func (s *sessionSource) appendSeedItems(items []surface.TimelineItem) {
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		event := seedEvent(s.session.Surface, item)
		if s.mergeSeedIntoAuthoritativeRow(event.ProviderKey, event.Operation, item) {
			continue
		}
		s.append(event)
	}
}

// seedEvent is the journal event that records a timeline item read from a
// provider's history.
func seedEvent(kind surface.SurfaceKind, item surface.TimelineItem) surface.StreamEvent {
	at, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
	providerKey := "timeline:" + item.ID
	if kind == surface.KindCodex && strings.HasPrefix(item.ID, "codex:") {
		providerKey = item.ID
	}
	operation := "upsert"
	if item.Kind == "done" {
		operation = "phase"
	}
	return surface.StreamEvent{
		Role:             item.Role,
		Origin:           item.Origin,
		Sender:           item.Sender,
		Title:            item.Title,
		Status:           item.Status,
		ResetsAt:         item.ResetsAt,
		Truncated:        item.Truncated,
		TruncationReason: item.TruncationReason,
		ID:               item.ID,
		ProviderKey:      providerKey,
		Version:          uint64(len(item.Text)),
		Operation:        operation,
		TurnID:           item.TurnID,
		CallID:           item.CallID,
		Attachment:       item.Attachment,
		Files:            item.Files,
		Timestamp:        at,
		Kind:             item.Kind,
		Text:             item.Text,
		Turn:             item.Turn,
	}
}

func (s *sessionSource) mergeSeedIntoAuthoritativeRow(providerKey, operation string, item surface.TimelineItem) bool {
	journalKey := providerKey
	if operation == "phase" {
		journalKey += ":phase:" + item.Kind
	}
	entry, found, err := s.manager.registry.SessionJournalEntryByProviderKey(s.session.ID, journalKey)
	if err != nil || !found {
		return false
	}
	var payload sessionJournalPayload
	if json.Unmarshal(entry.Payload, &payload) != nil || payload.Kind != item.Kind {
		return false
	}
	body := payload.Body
	if payload.BodyRef != "" {
		if len(entry.FullBody) == 0 {
			return false
		}
		body = string(entry.FullBody)
	}
	if body != item.Text && (!item.Truncated || !strings.HasPrefix(body, item.Text)) {
		return false
	}
	if body != "" && item.Kind != "done" {
		s.mu.Lock()
		s.appendBodies[providerKey] = body
		s.mu.Unlock()
	}
	merged := payload
	mergeSeedField(&merged.Role, item.Role)
	mergeSeedField(&merged.Origin, item.Origin)
	mergeSeedField(&merged.Sender, item.Sender)
	mergeSeedField(&merged.Title, item.Title)
	mergeSeedField(&merged.Status, item.Status)
	mergeSeedField(&merged.TurnID, item.TurnID)
	mergeSeedField(&merged.CallID, item.CallID)
	if body == item.Text && item.Truncated {
		merged.Truncated = true
		if merged.TruncationReason == "" {
			merged.TruncationReason = item.TruncationReason
		}
	}
	if item.Attachment != nil {
		merged.Attachment = item.Attachment
	}
	if len(item.Files) > 0 {
		merged.Files = item.Files
	}
	if item.Turn != nil {
		merged.Turn = item.Turn
	}
	if merged.Role == payload.Role && merged.Origin == payload.Origin && merged.Sender == payload.Sender && merged.Title == payload.Title && merged.Status == payload.Status && merged.TurnID == payload.TurnID && merged.CallID == payload.CallID && merged.Truncated == payload.Truncated && merged.TruncationReason == payload.TruncationReason && reflect.DeepEqual(merged.Attachment, payload.Attachment) && reflect.DeepEqual(merged.Files, payload.Files) && reflect.DeepEqual(merged.Turn, payload.Turn) {
		return true
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return true
	}
	_, changed, err := s.manager.registry.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.session.ID, Kind: merged.Kind, ProviderKey: journalKey, Payload: encoded, BodyRef: payload.BodyRef, FullBody: entry.FullBody, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
	if err != nil {
		s.mu.Lock()
		s.appendFailed = true
		s.mu.Unlock()
		s.appendSourceError(fmt.Errorf("session journal append failed: %w", err))
	} else if changed {
		s.wakeFollowers()
	}
	return true
}

func mergeSeedField(target *string, value string) {
	if value != "" {
		*target = value
	}
}

func (s *sessionSource) append(event surface.StreamEvent) {
	if event.Context != nil {
		current := s.session
		if refreshed, err := s.manager.registry.Session(s.session.ID); err == nil {
			current = *refreshed
		}
		if provider, ok := s.adapter.(surface.ContextUsageProvider); ok {
			if usage, err := provider.ContextUsage(s.ctx, &current); err == nil && usage != nil {
				event.Context = usage
			}
		}
	}
	s.mu.Lock()
	providerKey := event.ProviderKey
	if strings.HasPrefix(providerKey, "renderer:") {
		providerKey = s.epoch + ":" + providerKey
	}
	if event.Cursor > 0 && providerKey != "" && event.Cursor <= s.appendCursors[providerKey] {
		s.mu.Unlock()
		return
	}
	payload, bodyKey := s.normalizeLocked(event)
	s.mu.Unlock()
	accumulated := payload.Body
	_, changed, err := s.persist(payload)
	if err != nil {
		s.mu.Lock()
		s.appendFailed = true
		s.mu.Unlock()
		s.appendSourceError(fmt.Errorf("session journal append failed: %w", err))
		return
	}
	s.mu.Lock()
	if bodyKey != "" {
		s.appendBodies[bodyKey] = accumulated
	}
	if event.Cursor > 0 && providerKey != "" && event.Cursor > s.appendCursors[providerKey] {
		if s.appendCursors == nil {
			s.appendCursors = map[string]uint64{}
		}
		s.appendCursors[providerKey] = event.Cursor
	}
	s.mu.Unlock()
	if changed {
		s.wakeFollowers()
	}
}

func (s *sessionSource) persist(payload sessionJournalPayload) (registry.SessionJournalEntry, bool, error) {
	entry, err := sessionJournalEntry(s.session.ID, payload)
	if err != nil {
		return registry.SessionJournalEntry{}, false, err
	}
	return s.manager.registry.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
}

// sessionJournalEntry encodes a journal payload. A body longer than the
// inline limit is kept whole behind a body reference when the journal can
// hold it, and is otherwise marked as not retained.
func sessionJournalEntry(sessionID string, payload sessionJournalPayload) (registry.SessionJournalEntry, error) {
	fullBody := []byte(payload.Body)
	if len(fullBody) <= sessionStreamBodyBytes {
		encoded, err := json.Marshal(payload)
		return registry.SessionJournalEntry{SessionID: sessionID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, ObservedAt: time.Now().UTC()}, err
	}
	prefix := fullBody[:sessionStreamBodyBytes]
	for !utf8.Valid(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	payload.Body = string(prefix)
	payload.Truncated = true
	if ref, refErr := newSessionBodyRef(); refErr == nil {
		payload.BodyRef = ref
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return registry.SessionJournalEntry{}, err
	}
	if payload.BodyRef == "" || len(encoded)+len(fullBody) > sessionJournalRetentionBytes {
		payload.BodyRef = ""
		payload.TruncationReason = "full_body_not_retained"
		encoded, err = json.Marshal(payload)
		fullBody = nil
	}
	return registry.SessionJournalEntry{SessionID: sessionID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, BodyRef: payload.BodyRef, FullBody: fullBody, ObservedAt: time.Now().UTC()}, err
}

var errSessionJournalGap = errors.New("the session journal no longer holds the rows after this cursor; reselect the session")

const sessionJournalFollowPage = 64

// readSessionJournalAfter hands yield the journal rows after *cursor in seq
// order, a page at a time, advancing *cursor past each row yield accepts. It
// reports false when yield stops it or the read fails.
func readSessionJournalAfter(store *registry.Registry, sessionID string, cursor *uint64, yield func(registry.SessionJournalEntry) bool) (bool, error) {
	for {
		window, err := store.SessionJournalAfter(sessionID, *cursor, sessionJournalFollowPage)
		if err != nil {
			return false, err
		}
		if window.Gap {
			return false, errSessionJournalGap
		}
		for _, entry := range window.Entries {
			if !yield(entry) {
				return false, nil
			}
			*cursor = entry.Seq
		}
		if len(window.Entries) < sessionJournalFollowPage {
			return true, nil
		}
	}
}

func (s *sessionSource) wakeFollowers() {
	s.mu.Lock()
	for _, follower := range s.followers {
		select {
		case follower <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
}

func newSessionBodyRef() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func (s *sessionSource) normalizeLocked(event surface.StreamEvent) (sessionJournalPayload, string) {
	providerKey := event.ProviderKey
	if strings.HasPrefix(providerKey, "renderer:") {
		providerKey = s.epoch + ":" + providerKey
	}
	itemID := event.ID
	if strings.HasPrefix(event.ProviderKey, "renderer:") {
		itemID = s.epoch + ":" + itemID
	}
	if event.Kind == "context" || event.Kind == "goal" || event.Kind == "commands" {
		itemID = "state:" + event.Kind
		providerKey = itemID
	}
	if itemID == "" {
		itemID = providerKey
	}
	if itemID == "" {
		s.anonymous++
		itemID = fmt.Sprintf("%s:anonymous:%d", s.epoch, s.anonymous)
		providerKey = itemID
	}
	if event.Operation == "phase" {
		itemID += ":phase:" + event.Kind
		if providerKey != "" {
			providerKey += ":phase:" + event.Kind
		}
	}
	op := event.Operation
	if op == "" {
		op = "append"
	}
	body := event.Text
	bodyKey := ""
	if providerKey != "" && !strings.HasPrefix(event.ProviderKey, "renderer:") && op == "upsert" && body != "" {
		bodyKey = providerKey
	}
	if op == "append" && providerKey != "" && !strings.HasPrefix(event.ProviderKey, "renderer:") {
		body = s.appendBodies[providerKey] + body
		bodyKey = providerKey
		op = "upsert"
	}
	at := event.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return sessionJournalPayload{ItemID: itemID, ProviderKey: providerKey, Version: event.Version, Op: op, Kind: event.Kind, TurnID: event.TurnID, CallID: event.CallID, Final: event.Final, TS: at.UTC().Format(time.RFC3339Nano), Body: body, Role: event.Role, Origin: event.Origin, Sender: event.Sender, Title: event.Title, Status: event.Status, ResetsAt: event.ResetsAt, Context: event.Context, Goal: event.Goal, Commands: commandsOf(event.Commands), Unavailable: unavailableOf(event.Commands), Attachment: event.Attachment, Files: event.Files, Truncated: event.Truncated, TruncationReason: event.TruncationReason, Turn: event.Turn}, bodyKey
}

// prepareStream waits under ctx for the session journal to be seeded, then
// returns a subscription that lives until its Cancel, not until ctx ends, so
// a reply can be followed after the request that prepared it returns.
func (m *sessionSourceManager) prepareStream(ctx context.Context, session *surface.Session, adapter surface.Surface) (sessionstream.Subscription, error) {
	subscription, err := m.subscribeContext(ctx, session, adapter)
	if err != nil {
		return sessionstream.Subscription{}, err
	}
	m.mu.Lock()
	source := m.sources[session.ID]
	m.mu.Unlock()
	if source == nil {
		subscription.Cancel()
		return sessionstream.Subscription{}, fmt.Errorf("session source disappeared while preparing stream")
	}
	select {
	case <-source.seeded:
	case <-ctx.Done():
		subscription.Cancel()
		return sessionstream.Subscription{}, ctx.Err()
	}
	if err := source.seedError(); err != nil {
		subscription.Cancel()
		return sessionstream.Subscription{}, fmt.Errorf("seed session source: %w", err)
	}
	window, err := m.registry.SessionJournalAfter(session.ID, 0, 1)
	if err != nil {
		subscription.Cancel()
		return sessionstream.Subscription{}, fmt.Errorf("capture session journal cursor: %w", err)
	}
	events := make(chan sessionstream.Event, 64)
	done := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() { close(done) })
		subscription.Cancel()
	}
	go func() {
		defer close(events)
		cursor := window.LatestSeq
		send := func(event sessionstream.Event) bool {
			select {
			case events <- event:
				return true
			case <-done:
				return false
			}
		}
		follow := func() bool {
			more, err := readSessionJournalAfter(m.registry, session.ID, &cursor, func(entry registry.SessionJournalEntry) bool {
				event, decodeErr := sessionstream.DecodePayload(entry.Seq, entry.Payload)
				if decodeErr != nil {
					send(sessionstream.Event{Seq: entry.Seq, Kind: "source-error", Reason: decodeErr.Error()})
					return false
				}
				return send(event)
			})
			if err != nil {
				send(sessionstream.Event{Seq: cursor, Kind: "source-error", Reason: err.Error()})
			}
			return more
		}
		for {
			select {
			case <-done:
				return
			case _, open := <-subscription.Wake:
				if !follow() || !open {
					return
				}
			}
		}
	}()
	return sessionstream.Subscription{Cursor: window.LatestSeq, Events: events, Cancel: cancel}, nil
}

func commandsOf(commands *surface.RuntimeCommands) []surface.RuntimeCommand {
	if commands == nil {
		return nil
	}
	return commands.Commands
}

func unavailableOf(commands *surface.RuntimeCommands) string {
	if commands == nil {
		return ""
	}
	return commands.Unavailable
}
