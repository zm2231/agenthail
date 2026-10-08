package voice

import (
	"context"
	"errors"
	"time"
)

var errVoiceOperationStale = errors.New("voice call changed during the operation")

// voiceRead is called with the service mutex held. Reads may finish after cancellation,
// but cannot change call state or retain the lifecycle mutex while they wait.
func voiceRead[T any](s *Service, ctx context.Context, read func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	stop := context.AfterFunc(s.background, cancel)
	defer stop()
	defer cancel()
	var zero T
	if s.readSlots == nil {
		s.readSlots = make(chan struct{}, 4)
	}
	slots := s.readSlots
	select {
	case slots <- struct{}{}:
	default:
		return zero, errors.New("voice session reads are busy")
	}
	attempt, phase, generation := s.state.State.AttemptID, s.state.State.Phase, s.targetGeneration
	session := s.state.State.Session
	type result struct {
		value T
		err   error
	}
	results := make(chan result, 1)
	s.mu.Unlock()
	go func() {
		defer func() { <-slots }()
		value, err := read(ctx)
		results <- result{value, err}
	}()
	var value T
	var err error
	select {
	case outcome := <-results:
		value, err = outcome.value, outcome.err
	case <-ctx.Done():
		err = ctx.Err()
	}
	s.mu.Lock()
	if s.closed || session != s.state.State.Session || attempt != s.state.State.AttemptID || phase != s.state.State.Phase || generation != s.targetGeneration {
		return zero, errVoiceOperationStale
	}
	if err == nil {
		err = ctx.Err()
	}
	return value, err
}

// voiceCall never abandons an effect. Its provider owns cancellation, while
// lifecycle actions remain able to acquire the service mutex.
func voiceCall[T any](s *Service, ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	attempt, phase, generation := s.state.State.AttemptID, s.state.State.Phase, s.targetGeneration
	session := s.state.State.Session
	s.mu.Unlock()
	value, err := call(ctx)
	s.mu.Lock()
	if s.closed || session != s.state.State.Session || attempt != s.state.State.AttemptID || phase != s.state.State.Phase || generation != s.targetGeneration {
		return value, errVoiceOperationStale
	}
	if err == nil {
		err = ctx.Err()
	}
	return value, err
}
