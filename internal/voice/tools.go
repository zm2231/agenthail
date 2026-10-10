package voice

import (
	"context"
	"time"
)

type toolCall struct {
	event                  Event
	key, attempt, threadID string
	ctx                    context.Context
	cancel                 context.CancelFunc
}

// Tool effects are serialized independently of event ingestion and speech.
// The caller holds the service mutex.
func (s *Service) dispatchDynamicToolCall(e Event) {
	requestID, _ := e.Params["requestId"].(string)
	threadID, _ := e.Params["threadId"].(string)
	if s.closed || requestID == "" || s.state.State.Session == nil || threadID != s.state.State.Session.ID || e.Params["namespace"] != "agenthail" {
		return
	}
	attempt := s.state.State.AttemptID
	key := attempt + ":" + threadID + ":" + requestID
	if s.toolInFlight == nil {
		s.toolInFlight = map[string]bool{}
		s.toolCancels = map[string]context.CancelFunc{}
	}
	if s.toolInFlight[key] {
		return
	}
	if result, ok := s.toolResults[key]; ok && result.delivered {
		return
	}
	s.toolInFlight[key] = true
	ctx, cancel := context.WithTimeout(s.background, 30*time.Second)
	s.toolCancels[key] = cancel
	s.toolQueue = append(s.toolQueue, toolCall{event: e, key: key, attempt: attempt, threadID: threadID, ctx: ctx, cancel: cancel})
	if !s.toolWorkerActive {
		s.toolWorkerActive = s.spawn(s.runToolCalls)
	}
}

func (s *Service) runToolCalls() {
	s.mu.Lock()
	defer s.unlock()
	defer func() { s.toolWorkerActive = false }()
	for len(s.toolQueue) > 0 {
		call := s.toolQueue[0]
		s.toolQueue[0] = toolCall{}
		s.toolQueue = s.toolQueue[1:]
		if !s.closed && s.state.State.Session != nil && s.state.State.Session.ID == call.threadID && s.state.State.AttemptID == call.attempt {
			if s.state.State.Phase != "connected" {
				call.cancel()
			}
			s.handleDynamicToolCall(call.ctx, call.event)
		} else if !s.closed {
			s.respondRetiredToolCall(call.event)
		}
		call.cancel()
		delete(s.toolInFlight, call.key)
		delete(s.toolCancels, call.key)
	}
}

func (s *Service) respondRetiredToolCall(e Event) {
	responder, ok := s.provider.(DynamicToolResponder)
	if !ok {
		return
	}
	requestID, _ := e.Params["requestId"].(string)
	threadID, _ := e.Params["threadId"].(string)
	ctx, cancel := context.WithTimeout(s.background, 5*time.Second)
	s.mu.Unlock()
	_ = responder.RespondDynamicToolCall(ctx, requestID, threadID, false, "The voice call changed during this tool operation. Check its outcome before repeating an action.")
	cancel()
	s.mu.Lock()
}

func (s *Service) cancelToolCalls() {
	for _, cancel := range s.toolCancels {
		cancel()
	}
}
