package voice

import (
	"context"
	"time"
)

// toolTimeout bounds a voice tool other than a send to a lead.
const toolTimeout = 8 * time.Second

// runTool runs one Codex tool call as service work, so a slow tool delays
// only its own answer. Once the service is closed it answers at once.
func (s *Service) runTool(e Event) {
	responder, ok := s.provider.(DynamicToolResponder)
	if !ok {
		return
	}
	requestID, _ := e.Params["requestId"].(string)
	threadID, _ := e.Params["threadId"].(string)
	namespace, _ := e.Params["namespace"].(string)
	tool, _ := e.Params["tool"].(string)
	arguments, _ := e.Params["arguments"].(map[string]any)
	s.mu.Lock()
	if requestID == "" || s.state.State.Session == nil || threadID != s.state.State.Session.ID || namespace != "agenthail" {
		s.unlock()
		return
	}
	source := s.state.State.Session.ID
	callCtx, cancel := s.callContext(s.background)
	started := s.spawn(func() {
		defer cancel()
		timeout := toolTimeout
		if tool == "voice_send_to_lead" {
			timeout = leadSendTimeout
		}
		ctx, done := context.WithTimeout(callCtx, timeout)
		defer done()
		message, err := s.callTool(ctx, tool, arguments, source)
		answerCtx, answered := context.WithTimeout(context.WithoutCancel(ctx), toolTimeout)
		defer answered()
		if err != nil {
			_ = responder.RespondDynamicToolCall(answerCtx, requestID, threadID, false, err.Error())
			return
		}
		if err := responder.RespondDynamicToolCall(answerCtx, requestID, threadID, true, message); err != nil {
			s.mu.Lock()
			if s.state.State.Session == nil || s.state.State.Session.ID != source {
				s.unlock()
				return
			}
			s.state.State.Message = "Voice tool response could not be returned to Codex: " + err.Error()
			_ = s.commit()
		}
	})
	s.unlock()
	if !started {
		cancel()
		ctx, done := context.WithTimeout(context.Background(), toolTimeout)
		defer done()
		_ = responder.RespondDynamicToolCall(ctx, requestID, threadID, false, "Voice is shutting down.")
	}
}
