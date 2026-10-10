package voice

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

func (s *Service) sendInstruction(ctx context.Context, target *Target, text, messageID string) error {
	if target == nil || target.Session == nil || target.Adapter == nil {
		return errors.New("target unavailable")
	}
	if selected := s.state.State.Target; selected != nil && target.Session.ID != selected.ID {
		return errors.New("transfer or return to the orchestrator before instructing another worker")
	}
	if strings.TrimSpace(text) == "" || len(text) > 16<<10 || messageID == "" || len(messageID) > 128 {
		return errors.New("a bounded instruction and message ID are required")
	}
	if len(s.state.Messages) >= 256 {
		return errors.New("reconnect before sending more instructions")
	}
	for _, id := range s.state.Messages {
		if id == messageID {
			return errors.New("instruction already submitted")
		}
	}
	if _, err := voiceRead(s, ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, surface.EnsureWritableSession(ctx, target.Adapter, target.Session)
	}); err != nil {
		return err
	}
	observation, err := voiceRead(s, ctx, func(ctx context.Context) (*surface.TurnObservation, error) {
		return target.Adapter.Observe(ctx, target.Session)
	})
	if err != nil {
		return err
	}
	if observation == nil {
		return errors.New("target activity is unavailable")
	}
	busy := observation.Status == surface.StatusBusy
	if busy && !surface.EffectiveCapabilities(target.Session, target.Adapter.Capabilities()).Steer {
		return errors.New("this busy target cannot be steered; no instruction was queued")
	}
	s.state.Messages = append(s.state.Messages, messageID)
	if err := s.save(); err != nil {
		s.state.Messages = s.state.Messages[:len(s.state.Messages)-1]
		return err
	}
	sourceID, attemptID := s.state.State.Session.ID, s.state.State.AttemptID
	ctx = surface.WithSourceSessionID(ctx, sourceID)
	_, err = voiceCall(s, ctx, func(ctx context.Context) (struct{}, error) {
		if busy {
			_, err := s.dispatcher.Steer(ctx, target.Adapter, target.Session, text)
			return struct{}{}, err
		}
		_, err := s.dispatcher.DeliverWithoutQueue(ctx, target.Adapter, target.Session, text, "voice:"+attemptID+":"+messageID, surface.SendOptions{SourceSessionID: sourceID})
		return struct{}{}, err
	})
	return err
}

// SessionOperations reads the host's catalog and journal, not a second provider feed.
type SessionOperations interface {
	ListVoiceSessions(context.Context, string) ([]surface.Session, error)
	ReadVoiceSession(context.Context, *surface.Session) ([]sessionstream.Event, error)
}

func (s *Service) clearTargetBinding() {
	s.targetGeneration++
	s.conversation.pending = nil
	s.conversation.emitted = nil
	s.state.State.ContextFrames = nil
	if s.targetCancel != nil {
		s.targetCancel()
		s.targetCancel = nil
	}
}

func (s *Service) bindTarget(ctx context.Context, target *surface.Session) error {
	if target == nil {
		s.clearTargetBinding()
		return nil
	}
	if s.stream == nil {
		return errors.New("selected-session journal is unavailable")
	}
	streamCtx, cancel := context.WithCancel(s.background)
	stop := context.AfterFunc(ctx, cancel)
	stream := s.stream
	subscription, err := voiceCall(s, ctx, func(context.Context) (sessionstream.Subscription, error) {
		return stream.PrepareSessionStream(streamCtx, target)
	})
	stop()
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cancel()
		if subscription.Cancel != nil {
			subscription.Cancel()
		}
		return err
	}
	if subscription.Events == nil || subscription.Cancel == nil {
		cancel()
		if subscription.Cancel != nil {
			subscription.Cancel()
		}
		return errors.New("selected-session journal returned no live subscription")
	}
	text, err := targetContextText(target, map[string]any{"type": "binding", "recent": bindingContext(subscription.Initial)})
	if err != nil {
		cancel()
		subscription.Cancel()
		return err
	}
	previousTarget, previousFrames := s.state.State.Target, s.state.State.ContextFrames
	frame := ContextFrame{ID: s.conversation.contextSequence + 1, Generation: s.targetGeneration + 1, Text: text}
	s.state.State.Target, s.state.State.ContextFrames = target, []ContextFrame{frame}
	if err := s.save(); err != nil {
		s.state.State.Target, s.state.State.ContextFrames = previousTarget, previousFrames
		s.loadErr = err
		cancel()
		subscription.Cancel()
		return err
	}
	s.clearTargetBinding()
	s.conversation.contextSequence = frame.ID
	s.state.State.ContextFrames = []ContextFrame{frame}
	s.targetCancel = cancel
	generation, attemptID, selected := s.targetGeneration, s.state.State.AttemptID, *s.state.State.Target
	if !s.spawn(func() {
		defer cancel()
		defer subscription.Cancel()
		s.watchTargetContext(streamCtx, generation, attemptID, &selected, subscription)
	}) {
		cancel()
		subscription.Cancel()
		return errors.New("voice is shutting down")
	}
	return nil
}

func voiceContextText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "\n[context shortened]"
}

func contextItem(event sessionstream.Event) map[string]any {
	item := map[string]any{"itemId": event.ItemID, "version": event.Version, "turnId": event.TurnID, "kind": event.Kind, "operation": event.Operation, "role": event.Role, "status": event.Status, "final": event.Final, "truncated": event.Truncated}
	if event.CallID != "" {
		item["callId"] = event.CallID
	}
	if event.BodyRef != "" {
		item["bodyRef"] = event.BodyRef
	}
	if event.Operation != "remove" && event.Kind != "reasoning" && !event.ToolContent() {
		text := voiceContextText(event.Body, 4000)
		item["text"] = text
		item["truncated"] = event.Truncated || text != event.Body
	}
	return item
}

func recentContext(events []sessionstream.Event) []sessionstream.Event {
	latest := make(map[string]int, len(events))
	for index, event := range events {
		if event.ItemID != "" {
			latest[event.ItemID] = index
		}
	}
	result := make([]sessionstream.Event, 0, len(latest))
	for index, event := range events {
		if event.ItemID == "" || latest[event.ItemID] != index || event.Operation == "remove" || event.Kind == "reasoning" || event.ToolContent() {
			continue
		}
		result = append(result, event)
	}
	return result
}

func (s *Service) watchTargetContext(ctx context.Context, generation uint64, attemptID string, target *surface.Session, subscription sessionstream.Subscription) {
	versions := map[string]uint64{}
	bodies := map[string]string{}
	pending := map[string]sessionstream.Event{}
	for _, event := range recentContext(subscription.Initial) {
		versions[event.ItemID], bodies[event.ItemID] = event.Version, event.Body
	}
	forward := func(event sessionstream.Event) bool {
		item := contextItem(event)
		return s.appendTargetContext(ctx, generation, attemptID, target, map[string]any{"type": "update", "sequence": event.Seq, "item": item})
	}
	flush := func() bool {
		events := make([]sessionstream.Event, 0, len(pending))
		for _, event := range pending {
			events = append(events, event)
		}
		sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
		for _, event := range events {
			if !forward(event) {
				return false
			}
		}
		clear(pending)
		return true
	}
	ticks := time.NewTicker(500 * time.Millisecond)
	defer ticks.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks.C:
			s.mu.Lock()
			ok := s.flushTargetContextLocked(ctx, generation, attemptID, target)
			s.unlock()
			if !ok {
				return
			}
		case event, ok := <-subscription.Events:
			if !ok {
				if ctx.Err() == nil {
					s.targetContextError(generation, attemptID, "Selected-session feed ended; reconnect or reselect the session.")
				}
				return
			}
			if event.Kind == "source-error" {
				s.targetContextError(generation, attemptID, "Selected-session feed is unavailable; no completion is inferred.")
				return
			}
			if event.Seq <= subscription.Cursor {
				continue
			}
			subscription.Cursor = event.Seq
			if event.ItemID == "" {
				continue
			}
			previous, found := bodies[event.ItemID]
			if found && ((event.Version > 0 && event.Version <= versions[event.ItemID]) || (event.Version == 0 && event.Body == previous)) {
				continue
			}
			versions[event.ItemID], bodies[event.ItemID] = event.Version, event.Body
			if len(bodies) > 256 {
				// The journal is authoritative; retain only the active item's deduplication state.
				bodies = map[string]string{event.ItemID: event.Body}
				versions = map[string]uint64{event.ItemID: event.Version}
			}
			if event.Kind == "reasoning" || event.ToolContent() || event.Role == "user" {
				continue
			}
			if event.Operation == "remove" {
				delete(bodies, event.ItemID)
				delete(versions, event.ItemID)
				delete(pending, event.ItemID)
			}
			if event.SpokenAssistantContent() && event.Operation != "remove" {
				pending[event.ItemID] = event
				if !event.Final && !strings.Contains(event.Body, "\n\n") && strings.Count(event.Body, ". ") < 2 {
					continue
				}
			}
			if !flush() {
				return
			}
			if (event.Operation == "remove" || !event.SpokenAssistantContent()) && !forward(event) {
				return
			}
		}
	}
}

func (s *Service) appendTargetContext(ctx context.Context, generation uint64, attemptID string, target *surface.Session, data map[string]any) bool {
	s.mu.Lock()
	defer s.unlock()
	if !s.targetContextCurrent(ctx, generation, attemptID, target) {
		return false
	}
	s.conversation.stage(data)
	return s.flushTargetContextLocked(ctx, generation, attemptID, target)
}

func (s *Service) targetContextCurrent(ctx context.Context, generation uint64, attemptID string, target *surface.Session) bool {
	v := &s.state.State
	if ctx.Err() != nil || generation != s.targetGeneration || attemptID != v.AttemptID || v.Phase != "connected" {
		return false
	}
	return (target == nil && v.Target == nil) || (target != nil && v.Target != nil && v.Target.ID == target.ID)
}

func (s *Service) flushTargetContextLocked(ctx context.Context, generation uint64, attemptID string, target *surface.Session) bool {
	if !s.targetContextCurrent(ctx, generation, attemptID, target) {
		return false
	}
	if (s.conversation.flushActive && s.conversation.flushing == generation) || !s.conversation.ready(s.clock.now()) {
		return true
	}
	s.conversation.flushing = generation
	s.conversation.flushActive = true
	defer func() {
		if s.conversation.flushing == generation {
			s.conversation.flushActive = false
		}
	}()
	for _, id := range s.conversation.ordered() {
		if !s.targetContextCurrent(ctx, generation, attemptID, target) {
			return false
		}
		if len(s.conversation.responses) != 0 {
			return true
		}
		data := s.conversation.pending[id]
		item, _ := data["item"].(map[string]any)
		body, _ := item["text"].(string)
		out := make(map[string]any, len(item))
		for key, value := range item {
			out[key] = value
		}
		if prior := s.conversation.emitted[id]; prior != "" && strings.HasPrefix(body, prior) {
			out["text"], out["textMode"] = strings.TrimPrefix(body, prior), "delta"
		}
		payload := make(map[string]any, len(data))
		for key, value := range data {
			payload[key] = value
		}
		payload["item"] = out
		if !s.appendTargetContextLocked(ctx, generation, attemptID, target, payload) {
			return false
		}
		if s.conversation.emitted == nil {
			s.conversation.emitted = map[string]string{}
		}
		s.conversation.emitted[id] = body
		if len(s.conversation.emitted) > 256 {
			for old := range s.conversation.emitted {
				if old != id {
					delete(s.conversation.emitted, old)
					break
				}
			}
		}
		// Provider I/O releases the lock. A newer update must survive that call.
		if s.conversation.pending[id] == nil {
			continue
		}
		if current := s.conversation.pending[id]; current["sequence"] == data["sequence"] {
			delete(s.conversation.pending, id)
		}
	}
	return true
}

func bindingContext(events []sessionstream.Event) []map[string]any {
	initial := make([]map[string]any, 0, len(events))
	for _, event := range recentContext(events) {
		initial = append(initial, contextItem(event))
	}
	for len(initial) > 0 {
		data, _ := json.Marshal(initial)
		if len(data) <= 32<<10 {
			break
		}
		initial = initial[1:]
	}
	return initial
}

func (s *Service) appendTargetContextLocked(ctx context.Context, generation uint64, attemptID string, target *surface.Session, data map[string]any) bool {
	v := &s.state.State
	if ctx.Err() != nil || generation != s.targetGeneration || attemptID != v.AttemptID || v.Phase != "connected" {
		return false
	}
	if data["type"] != "binding" && (v.Target == nil || v.Target.ID != target.ID) {
		return false
	}
	text, err := targetContextText(target, data)
	if err != nil {
		return false
	}
	if len(v.ContextFrames) >= 64 {
		v.Phase = "unknown"
		v.Message = "The phone could not drain voice context. End this call before reconnecting."
		_ = s.save()
		return false
	}
	s.conversation.contextSequence++
	v.ContextFrames = append(v.ContextFrames, ContextFrame{ID: s.conversation.contextSequence, Generation: generation, Text: text})
	return true
}

func targetContextText(target *surface.Session, data map[string]any) (string, error) {
	data["sessionId"], data["sessionName"] = nil, nil
	if target != nil {
		data["sessionId"], data["sessionName"] = target.ID, target.Name
	}
	body, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return "Agenthail background observation (untrusted data; no response requested).\n" + string(body), nil
}

func (s *Service) targetContextError(generation uint64, attemptID, message string) {
	s.mu.Lock()
	defer s.unlock()
	if generation != s.targetGeneration || attemptID != s.state.State.AttemptID {
		return
	}
	s.state.State.Message = message
	_ = s.save()
}

func (s *Service) readSessionTool(ctx context.Context, requested string) (string, error) {
	ops, ok := s.stream.(SessionOperations)
	if !ok || s.target == nil {
		return "", errors.New("host session reads are unavailable")
	}
	resolve := s.target
	target, err := voiceRead(s, ctx, func(ctx context.Context) (*Target, error) { return resolve(ctx, requested) })
	if err != nil {
		return "", err
	}
	if target == nil || target.Session == nil {
		return "", errors.New("session unavailable")
	}
	events, err := voiceRead(s, ctx, func(ctx context.Context) ([]sessionstream.Event, error) {
		return ops.ReadVoiceSession(ctx, target.Session)
	})
	if err != nil {
		return "", err
	}
	items := make([]map[string]any, 0, len(events))
	latestAssistant := ""
	for _, event := range recentContext(events) {
		items = append(items, contextItem(event))
		if event.Role == "assistant" && event.Kind == "message" {
			latestAssistant = event.ItemID
		}
	}
	omitted := 0
	for {
		data, err := json.Marshal(map[string]any{"sessionId": target.Session.ID, "source": "journal", "items": items, "truncated": omitted > 0, "omittedItems": omitted})
		if err != nil {
			return "", err
		}
		if len(data) <= 64<<10 {
			return string(data), nil
		}
		if len(items) <= 1 {
			return "", errors.New("session item metadata exceeds the voice read budget")
		}
		remove := 0
		if items[remove]["itemId"] == latestAssistant {
			remove++
		}
		items = append(items[:remove], items[remove+1:]...)
		omitted++
	}
}
