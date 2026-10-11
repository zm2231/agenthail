package voice

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
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
	s.workerItems = nil
	s.state.State.ContextFrames = nil
	s.retireDelegations()
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
	if err != nil || subscription.Events == nil || subscription.Cancel == nil {
		cancel()
		if subscription.Cancel != nil {
			subscription.Cancel()
		}
		if err != nil {
			return err
		}
		return errors.New("selected-session journal returned no live subscription")
	}
	previousTarget := s.state.State.Target
	s.state.State.Target = target
	if err := s.save(); err != nil {
		s.state.State.Target = previousTarget
		cancel()
		subscription.Cancel()
		return err
	}
	s.clearTargetBinding()
	s.workerItems = make(map[string]sessionstream.Event)
	for _, event := range recentContext(subscription.Initial) {
		s.workerItems[event.ItemID] = event
	}
	s.targetCancel = cancel
	generation, attempt, selected := s.targetGeneration, s.state.State.AttemptID, *target
	if !s.spawn(func() {
		defer cancel()
		defer subscription.Cancel()
		s.watchTargetContext(streamCtx, generation, attempt, &selected, subscription)
	}) {
		cancel()
		subscription.Cancel()
		return errors.New("voice is shutting down")
	}
	return nil
}

func (s *Service) watchTargetContext(ctx context.Context, generation uint64, attempt string, target *surface.Session, subscription sessionstream.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-subscription.Events:
			if !ok || event.Kind == "source-error" {
				if ctx.Err() == nil {
					s.targetContextError(generation, attempt, "Selected-session feed unavailable; no completion is inferred.")
				}
				return
			}
			if event.Seq <= subscription.Cursor {
				continue
			}
			subscription.Cursor = event.Seq
			s.mu.Lock()
			if !s.targetContextCurrent(ctx, generation, attempt, target) {
				s.unlock()
				return
			}
			s.retainWorkerItem(event)
			s.unlock()
		}
	}
}

func (s *Service) retainWorkerItem(event sessionstream.Event) {
	if event.ItemID == "" {
		return
	}
	if event.Kind == "reasoning" || event.ToolContent() {
		event.Body = ""
	}
	if s.workerItems == nil {
		s.workerItems = make(map[string]sessionstream.Event)
	}
	previous, exists := s.workerItems[event.ItemID]
	if exists && event.Version > 0 && event.Version <= previous.Version {
		return
	}
	if event.Operation == "remove" {
		delete(s.workerItems, event.ItemID)
		return
	}
	s.workerItems[event.ItemID] = event
	if len(s.workerItems) > 64 {
		oldest := event.ItemID
		for id, item := range s.workerItems {
			if item.Seq < s.workerItems[oldest].Seq {
				oldest = id
			}
		}
		delete(s.workerItems, oldest)
	}
}

func (s *Service) targetContextCurrent(ctx context.Context, generation uint64, attempt string, target *surface.Session) bool {
	v := &s.state.State
	return ctx.Err() == nil && generation == s.targetGeneration && attempt == v.AttemptID &&
		(v.Phase == "connected" || v.Phase == "starting" || v.Phase == "negotiating") && target != nil && v.Target != nil && target.ID == v.Target.ID
}

func (s *Service) retainedWorkerContext() []sessionstream.Event {
	items := make([]sessionstream.Event, 0, len(s.workerItems))
	for _, item := range s.workerItems {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
	return items
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
