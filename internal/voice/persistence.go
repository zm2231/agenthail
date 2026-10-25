package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
)

type diskState struct {
	State                   State           `json:"state"`
	Owner                   string          `json:"owner"`
	Messages                []string        `json:"messages"`
	Requests                []requestRecord `json:"requests,omitempty"`
	NativeHeartbeatEnrolled bool            `json:"nativeHeartbeatEnrolled,omitempty"`
	// ToolsNoticeShown records that a call ended while the outdated tools
	// notice was shown, so it is shown once per operator.
	ToolsNoticeShown bool `json:"toolsNoticeShown,omitempty"`
}

// stage records the state for the next flush. The caller holds s.mu.
func (s *Service) stage() {
	copy := s.state
	copy.State.SDP = ""
	data, _ := json.Marshal(copy)
	s.generation++
	s.unsaved = data
}

// stateSaveTimeout bounds how long a caller waits for the state file.
const stateSaveTimeout = 5 * time.Second

// flush writes the newest staged state outside s.mu. Writes are serialized,
// and one that finds a newer state already written does nothing.
func (s *Service) flush() error {
	ctx, cancel := context.WithTimeout(context.Background(), stateSaveTimeout)
	defer cancel()
	select {
	case s.saving <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("save voice state: %w", ctx.Err())
	}
	s.mu.Lock()
	data, generation := s.unsaved, s.generation
	s.mu.Unlock()
	if generation <= s.written {
		<-s.saving
		return nil
	}
	done := make(chan error, 1)
	go func() {
		err := writeState(s.path, data)
		if err == nil {
			s.written = generation
		}
		<-s.saving
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("save voice state: %w", ctx.Err())
	}
}

// commit stages the state, releases s.mu and flushes. A failed write blocks
// later actions other than hangup, as an unreadable state file does.
func (s *Service) commit() error {
	s.stage()
	s.unlock()
	if err := s.flush(); err != nil {
		s.mu.Lock()
		s.loadErr = err
		s.unlock()
		return err
	}
	return nil
}

func writeState(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return atomicfile.WriteSynced(path, data, 0o600)
}
