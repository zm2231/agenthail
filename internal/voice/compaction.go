package voice

import "context"

// refreshBrief gives voice a fresh brief when the session compacted since its last one.
func (s *Service) refreshBrief(ctx context.Context, f *follower) {
	s.mu.Lock()
	briefer, given := s.briefer, s.state.State.Brief
	if briefer == nil || s.follow != f || given == nil || given.SessionID != f.target.ID {
		s.unlock()
		return
	}
	s.unlock()
	src := briefer.BriefSource(ctx, &f.target)
	if src.Compactions == nil {
		return
	}
	text, info := RenderBrief(src)
	s.mu.Lock()
	v := &s.state.State
	if s.follow != f || v.Brief == nil || v.Brief.SessionID != f.target.ID {
		s.unlock()
		return
	}
	compacted := v.Brief.Compactions != nil && *src.Compactions > *v.Brief.Compactions
	if !compacted && v.Brief.Compactions != nil {
		s.unlock()
		return
	}
	v.Brief = &info
	if compacted {
		s.appendEvent(Event{Method: "voice/target/compacted", Params: map[string]any{"targetId": f.target.ID, "compactions": *src.Compactions}})
	}
	session := v.Session
	s.stage()
	s.unlock()
	if err := s.flush(); err != nil || !compacted {
		return
	}
	s.note(f, session, "The session compacted its context, so details from before may be gone from its memory. Its brief, read again:\n\n"+text)
}
