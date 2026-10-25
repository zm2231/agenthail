package daemon

type sessionSourceSubscription struct {
	Wake   <-chan struct{}
	Cancel func()
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
