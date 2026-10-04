package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	sessionPageHandoffGrace      = 5 * time.Second
	sessionJournalSeedTimeout    = 12 * time.Second
)

var (
	sessionSeedRetryDelay          = 2 * time.Second
	sessionSeedRetryMaxDelay       = time.Minute
	sessionSnapshotRefreshInterval = 5 * time.Second
)

type sessionJournalPayload struct {
	Context          *surface.ContextUsage `json:"context,omitempty"`
	Goal             *surface.GoalState    `json:"goal"`
	Role             string                `json:"role,omitempty"`
	Title            string                `json:"title,omitempty"`
	Status           string                `json:"status,omitempty"`
	ItemID           string                `json:"itemId"`
	ProviderKey      string                `json:"providerKey,omitempty"`
	Version          uint64                `json:"version"`
	Op               string                `json:"op"`
	Kind             string                `json:"kind"`
	TurnID           string                `json:"turnId,omitempty"`
	CallID           string                `json:"callId,omitempty"`
	Final            bool                  `json:"final,omitempty"`
	TS               string                `json:"ts"`
	Body             string                `json:"body,omitempty"`
	Truncated        bool                  `json:"truncated"`
	TruncationReason string                `json:"truncationReason,omitempty"`
	BodyRef          string                `json:"bodyRef,omitempty"`
	Reason           string                `json:"reason,omitempty"`
	Attachment       *surface.Attachment   `json:"attachment,omitempty"`
}

type sessionSourceManager struct {
	registry *registry.Registry
	mu       sync.Mutex
	sources  map[string]*sessionSource
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
	subscribers     map[uint64]chan registry.SessionJournalEntry
	nextID          uint64
	holders         map[string]int
	appendBodies    map[string]string
	appendCursors   map[string]uint64
	streamCursor    uint64
	streamCursorSet bool
	streamCursorErr error
	anonymous       uint64
	sourceVersion   uint64
}

type sessionSourceSubscription struct {
	Entries <-chan registry.SessionJournalEntry
	Cancel  func()
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
	source := m.sources[session.ID]
	start := false
	if source == nil {
		epoch, err := m.registry.BeginSessionJournalSource(session.ID)
		if err != nil {
			m.mu.Unlock()
			return sessionSourceSubscription{}, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		source = &sessionSource{manager: m, session: *session, adapter: adapter, epoch: epoch, ctx: ctx, cancel: cancel, seeded: make(chan struct{}), subscribers: map[uint64]chan registry.SessionJournalEntry{}, holders: map[string]int{}, appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
		m.sources[session.ID] = source
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
	source := m.sources[session.ID]
	start := false
	if source == nil {
		epoch, err := m.registry.BeginSessionJournalSource(session.ID)
		if err != nil {
			m.mu.Unlock()
			return nil, nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		source = &sessionSource{manager: m, session: *session, adapter: adapter, epoch: epoch, ctx: ctx, cancel: cancel, seeded: make(chan struct{}), subscribers: map[uint64]chan registry.SessionJournalEntry{}, holders: map[string]int{}, appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
		m.sources[session.ID] = source
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
	events := make(chan registry.SessionJournalEntry, 64)
	s.subscribers[id] = events
	s.holders[holder]++
	s.mu.Unlock()
	return sessionSourceSubscription{Entries: events, Cancel: func() {
		s.mu.Lock()
		if existing, found := s.subscribers[id]; found {
			delete(s.subscribers, id)
			close(existing)
		}
		s.holders[holder]--
		if s.holders[holder] <= 0 {
			delete(s.holders, holder)
		}
		stop := len(s.subscribers) == 0 && len(s.holders) == 0
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
	stop := len(s.subscribers) == 0 && len(s.holders) == 0
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
	if len(s.subscribers) != 0 || len(s.holders) != 0 {
		return
	}
	s.cancel()
	if s.manager.sources[s.session.ID] == s {
		delete(s.manager.sources, s.session.ID)
	}
}

func (m *sessionSourceManager) shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, source := range m.sources {
		source.cancel()
		delete(m.sources, id)
	}
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
		s.closeSubscribers()
		s.remove()
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
	if !surface.EffectiveCapabilities(&s.session, s.adapter.Capabilities()).Stream {
		for s.wait(sessionSnapshotRefreshInterval) {
			s.setSeedErr(s.seedJournal())
		}
		return
	}
	for {
		current := s.session
		if refreshed, refreshErr := s.manager.registry.Session(s.session.ID); refreshErr == nil {
			current = *refreshed
		}
		current.StreamCursor = s.streamCursor
		current.StreamCursorSet = s.streamCursorSet
		current.TranscriptOffset = s.session.TranscriptOffset
		current.TranscriptOffsetSet = s.session.TranscriptOffsetSet
		current.TranscriptIdentity = s.session.TranscriptIdentity
		current.CodexPendingEventUser = s.session.CodexPendingEventUser
		current.CodexPendingEventTurn = s.session.CodexPendingEventTurn
		current.CodexCurrentTurnID = s.session.CodexCurrentTurnID
		streamErr := s.adapter.Stream(s.ctx, &current, "", s.append, 30*time.Minute)
		if current.Transcript == s.session.Transcript && current.TranscriptIdentity != "" && current.TranscriptIdentity == s.session.TranscriptIdentity && current.TranscriptOffsetSet && current.TranscriptOffset >= s.session.TranscriptOffset {
			s.session.TranscriptOffset = current.TranscriptOffset
			s.session.TranscriptOffsetSet = true
			s.session.TranscriptIdentity = current.TranscriptIdentity
			s.session.CodexPendingEventUser = current.CodexPendingEventUser
			s.session.CodexPendingEventTurn = current.CodexPendingEventTurn
			s.session.CodexCurrentTurnID = current.CodexCurrentTurnID
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

func (s *sessionSource) closeSubscribers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, subscriber := range s.subscribers {
		delete(s.subscribers, id)
		close(subscriber)
	}
}

func (s *sessionSource) remove() {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	if s.manager.sources[s.session.ID] == s {
		delete(s.manager.sources, s.session.ID)
	}
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

func (m *sessionSourceManager) refresh(session *surface.Session, adapter surface.Surface) {
	release, err := m.hold(session, adapter, "page-refresh")
	if err != nil {
		return
	}
	time.AfterFunc(sessionPageHandoffGrace, release)
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
	entry, changed, err := s.manager.registry.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.session.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
	if err != nil {
		return
	}
	if changed {
		s.publish(entry)
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
	streamable := surface.EffectiveCapabilities(&s.session, s.adapter.Capabilities()).Stream
	if statusErr == nil && seedStatus == registry.SessionJournalSeeded && !localTranscript && streamable {
		return nil
	}
	if localTranscript && trustedIdentity == "" && seedSeq > 0 {
		return fmt.Errorf("Codex local transcript identity checkpoint is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	ctx, cancel := context.WithTimeout(s.ctx, sessionJournalSeedTimeout)
	defer cancel()
	if seedStatus == registry.SessionJournalSeeded && localTranscript {
		return s.catchUpLocalTranscript(ctx, trustedIdentity)
	}
	read, err := surface.ReadSession(ctx, s.adapter, &s.session, surface.SessionReadRequest{Limit: 40})
	if err != nil || read == nil {
		if err == nil {
			err = fmt.Errorf("session source returned no activity result")
		}
		return err
	}
	if trustedIdentity != "" && read.TranscriptIdentity != trustedIdentity {
		return fmt.Errorf("Codex local transcript was replaced before catch-up: %w", surface.ErrTranscriptUnavailable)
	}
	if read.TranscriptOffsetSet {
		s.session.TranscriptOffset = read.TranscriptOffset
		s.session.TranscriptOffsetSet = true
		s.session.CodexPendingEventUser = read.CodexPendingEventUser
		s.session.CodexPendingEventTurn = read.CodexPendingEventTurn
		s.session.CodexCurrentTurnID = read.CodexCurrentTurnID
		s.session.TranscriptIdentity = read.TranscriptIdentity
	} else if localTranscript {
		return fmt.Errorf("Codex local transcript is unavailable")
	}
	if localTranscript && read.TranscriptIdentity == "" {
		return fmt.Errorf("Codex local transcript identity is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	s.appendSeedItems(read.Items)
	return s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, read.TranscriptIdentity)
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

func (s *sessionSource) catchUpLocalTranscript(ctx context.Context, trustedIdentity string) error {
	known, err := s.knownJournalItems()
	if err != nil {
		return fmt.Errorf("read journal seed identities: %w", err)
	}
	pages := make([][]surface.TimelineItem, 0, 2)
	before := int64(0)
	transcriptIdentity := trustedIdentity
	for {
		read, err := surface.ReadSession(ctx, s.adapter, &s.session, surface.SessionReadRequest{Before: before, Limit: 40})
		if err != nil || read == nil {
			if err == nil {
				err = fmt.Errorf("session source returned no activity result")
			}
			return err
		}
		if !read.TranscriptOffsetSet {
			return fmt.Errorf("Codex local transcript is unavailable")
		}
		if transcriptIdentity == "" {
			transcriptIdentity = read.TranscriptIdentity
		} else if read.TranscriptIdentity != transcriptIdentity {
			phase := "during catch-up"
			if len(pages) == 0 {
				phase = "before catch-up"
			}
			return fmt.Errorf("Codex local transcript was replaced %s: %w", phase, surface.ErrTranscriptUnavailable)
		}
		if read.TranscriptOffsetSet && !s.session.TranscriptOffsetSet {
			s.session.TranscriptOffset = read.TranscriptOffset
			s.session.TranscriptOffsetSet = true
			s.session.CodexPendingEventUser = read.CodexPendingEventUser
			s.session.CodexPendingEventTurn = read.CodexPendingEventTurn
			s.session.CodexCurrentTurnID = read.CodexCurrentTurnID
			s.session.TranscriptIdentity = read.TranscriptIdentity
		}
		pages = append(pages, read.Items)
		if timelineItemsOverlapJournal(read.Items, known) || read.NextBefore == 0 {
			break
		}
		before = read.NextBefore
	}
	for index := len(pages) - 1; index >= 0; index-- {
		s.appendSeedItems(pages[index])
	}
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, transcriptIdentity); err != nil {
		return err
	}
	return nil
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
		at, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
		providerKey := "timeline:" + item.ID
		if s.session.Surface == surface.KindCodex && strings.HasPrefix(item.ID, "codex:") {
			providerKey = item.ID
		}
		operation := "upsert"
		if item.Kind == "done" {
			operation = "phase"
		}
		if s.journalSupersedesSeed(providerKey, operation, item) {
			continue
		}
		if item.Text != "" && item.Kind != "done" {
			s.mu.Lock()
			s.appendBodies[providerKey] = item.Text
			s.mu.Unlock()
		}
		s.append(surface.StreamEvent{
			Role:        item.Role,
			Title:       item.Title,
			Status:      item.Status,
			Truncated:   item.Truncated,
			ID:          item.ID,
			ProviderKey: providerKey,
			Version:     uint64(len(item.Text)),
			Operation:   operation,
			TurnID:      item.TurnID,
			CallID:      item.CallID,
			Attachment:  item.Attachment,
			Timestamp:   at,
			Kind:        item.Kind,
			Text:        item.Text,
		})
	}
}

func (s *sessionSource) journalSupersedesSeed(providerKey, operation string, item surface.TimelineItem) bool {
	if operation == "phase" {
		providerKey += ":phase:" + item.Kind
	}
	entry, found, err := s.manager.registry.SessionJournalEntryByProviderKey(s.session.ID, providerKey)
	if err != nil || !found {
		return false
	}
	var payload sessionJournalPayload
	if json.Unmarshal(entry.Payload, &payload) != nil || payload.Kind != item.Kind {
		return false
	}
	if item.Attachment != nil && (payload.Attachment == nil || payload.Attachment.ID != item.Attachment.ID) {
		return false
	}
	body := payload.Body
	if payload.BodyRef != "" {
		if len(entry.FullBody) == 0 {
			return false
		}
		body = string(entry.FullBody)
	}
	return body == item.Text || (item.Truncated && strings.HasPrefix(body, item.Text))
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
	payload := s.normalizeLocked(event)
	if event.Cursor > 0 && providerKey != "" {
		if s.appendCursors == nil {
			s.appendCursors = map[string]uint64{}
		}
		s.appendCursors[providerKey] = event.Cursor
	}
	s.mu.Unlock()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fullBody := []byte(payload.Body)
	if len(fullBody) > sessionStreamBodyBytes {
		prefix := fullBody[:sessionStreamBodyBytes]
		for !utf8.Valid(prefix) {
			prefix = prefix[:len(prefix)-1]
		}
		payload.Body = string(prefix)
		payload.Truncated = true
		ref, refErr := newSessionBodyRef()
		if refErr == nil {
			payload.BodyRef = ref
		}
		encoded, err = json.Marshal(payload)
		if err != nil {
			return
		}
		if payload.BodyRef == "" || len(encoded)+len(fullBody) > sessionJournalRetentionBytes {
			payload.BodyRef = ""
			payload.TruncationReason = "full_body_not_retained"
			encoded, err = json.Marshal(payload)
			if err != nil {
				return
			}
			fullBody = nil
		}
		var entry registry.SessionJournalEntry
		var changed bool
		entry, changed, err = s.manager.registry.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.session.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, BodyRef: payload.BodyRef, FullBody: fullBody, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
		if errors.Is(err, registry.ErrSessionJournalEntryTooLarge) && payload.BodyRef != "" {
			payload.BodyRef = ""
			payload.TruncationReason = "full_body_not_retained"
			fullBody = nil
			encoded, err = json.Marshal(payload)
			if err == nil {
				entry, changed, err = s.manager.registry.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.session.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
			}
		}
		if err != nil {
			return
		}
		if changed {
			s.publish(entry)
		}
		return
	}
	entry, changed, err := s.manager.registry.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.session.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
	if err != nil {
		return
	}
	if changed {
		s.publish(entry)
	}
}

func (s *sessionSource) publish(entry registry.SessionJournalEntry) {
	s.mu.Lock()
	for id, subscriber := range s.subscribers {
		select {
		case subscriber <- entry:
		default:
			delete(s.subscribers, id)
			close(subscriber)
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

func (s *sessionSource) normalizeLocked(event surface.StreamEvent) sessionJournalPayload {
	providerKey := event.ProviderKey
	if strings.HasPrefix(providerKey, "renderer:") {
		providerKey = s.epoch + ":" + providerKey
	}
	itemID := event.ID
	if strings.HasPrefix(event.ProviderKey, "renderer:") {
		itemID = s.epoch + ":" + itemID
	}
	if event.Kind == "context" || event.Kind == "goal" {
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
	if providerKey != "" && !strings.HasPrefix(event.ProviderKey, "renderer:") && op == "upsert" && body != "" {
		s.appendBodies[providerKey] = body
	}
	if op == "append" && providerKey != "" && !strings.HasPrefix(event.ProviderKey, "renderer:") {
		s.appendBodies[providerKey] += body
		body = s.appendBodies[providerKey]
		op = "upsert"
	}
	at := event.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return sessionJournalPayload{ItemID: itemID, ProviderKey: providerKey, Version: event.Version, Op: op, Kind: event.Kind, TurnID: event.TurnID, CallID: event.CallID, Final: event.Final, TS: at.UTC().Format(time.RFC3339Nano), Body: body, Role: event.Role, Title: event.Title, Status: event.Status, Context: event.Context, Goal: event.Goal, Attachment: event.Attachment, Truncated: event.Truncated}
}

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
	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case entry, ok := <-subscription.Entries:
				if !ok {
					return
				}
				if entry.Seq <= window.LatestSeq {
					continue
				}
				event, decodeErr := sessionstream.DecodePayload(entry.Seq, entry.Payload)
				if decodeErr != nil {
					select {
					case events <- sessionstream.Event{Seq: entry.Seq, Kind: "source-error", Reason: decodeErr.Error()}:
					case <-ctx.Done():
					}
					return
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return sessionstream.Subscription{Cursor: window.LatestSeq, Events: events, Cancel: subscription.Cancel}, nil
}
