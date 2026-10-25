package voice

import (
	"encoding/json"
	"strings"
)

// snapshot is the state as of the last release of s.mu, so View never waits
// on the lock.
type snapshot struct {
	state                   State
	owner                   string
	active                  bool
	nativeHeartbeatEnrolled bool
}

func (p *snapshot) view(owner string) State {
	v := p.state
	v.Occupied = p.active && p.owner != owner
	if v.Occupied {
		v.SDP = ""
	}
	return v
}

func (s *Service) active() bool {
	switch s.state.State.Phase {
	case "starting", "negotiating", "connected", "stopping", "unknown":
		return true
	}
	return false
}

// View never waits on s.mu. The owner's view starts the event poll when no one
// holds the lock; a held lock means an action or the poll itself is running.
func (s *Service) View(owner string) State {
	current := s.published.Load()
	if current.owner == owner {
		if !current.nativeHeartbeatEnrolled {
			s.seen()
		}
		if current.active && s.mu.TryLock() {
			if s.active() && s.cursor.Source != "" && s.provider != nil && s.loadErr == nil {
				s.observe()
			}
			s.unlock()
			current = s.published.Load()
		}
	}
	return current.view(owner)
}

func (s *Service) view(owner string) State {
	return s.capture().view(owner)
}

// capture copies the state for readers outside s.mu. The caller holds s.mu.
func (s *Service) capture() *snapshot {
	v := s.state.State
	if s.loadErr != nil {
		v.Phase = "blocked"
		v.Message = "Voice state could not be read: " + s.loadErr.Error()
	}
	if s.provider == nil {
		v.Phase = "blocked"
		v.Message = "Codex voice is not configured on this host."
	}
	if s.toolsOutdated() {
		v.Message = strings.TrimSpace(v.Message + "\n\n" + toolsOutdatedNotice)
	}
	// HTTP encoding must not race the observer's mutation of the retained events.
	data, _ := json.Marshal(v)
	var copy State
	_ = json.Unmarshal(data, &copy)
	return &snapshot{state: copy, owner: s.state.Owner, active: s.active(), nativeHeartbeatEnrolled: s.state.NativeHeartbeatEnrolled}
}

// unlock publishes the state for View and releases s.mu.
func (s *Service) unlock() {
	s.syncOperatorSourceActive()
	s.published.Store(s.capture())
	s.mu.Unlock()
}

func (s *Service) seen() {
	s.lastSeen.Store(s.clock.now().UnixNano())
}

// appendEvent records an event of the call's own, keeping the latest 100.
// The caller holds s.mu.
func (s *Service) appendEvent(e Event) {
	s.state.State.Events = append(s.state.State.Events, e)
	if len(s.state.State.Events) > 100 {
		s.state.State.Events = s.state.State.Events[len(s.state.State.Events)-100:]
		s.state.State.Truncated = true
	}
}
