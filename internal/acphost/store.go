package acphost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/surface"
)

var ErrTranscriptGone = errors.New("the session's transcript file is gone")

type Store struct{ Root string }

func DefaultStore(home string) Store { return Store{Root: filepath.Join(home, ".agenthail", "acp")} }

func (s Store) dir(agent string) string              { return filepath.Join(s.Root, agent) }
func (s Store) RecordPath(agent, id string) string     { return filepath.Join(s.dir(agent), id+".json") }
func (s Store) TranscriptPath(agent, id string) string { return filepath.Join(s.dir(agent), id+".jsonl") }
func (s Store) lockPath(agent, id string) string       { return filepath.Join(s.dir(agent), id+".lock") }

func (s Store) WriteRecord(state State) error {
	if err := os.MkdirAll(s.dir(state.Agent), 0o700); err != nil {
		return err
	}
	state.Requests = nil
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.RecordPath(state.Agent, state.SessionID), data, 0o600)
}

func (s Store) ReadRecord(agent, id string) (State, error) {
	data, err := os.ReadFile(s.RecordPath(agent, id))
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("read session record %s: %w", s.RecordPath(agent, id), err)
	}
	return state, nil
}

func (s Store) Records(agent string) ([]State, error) {
	entries, err := os.ReadDir(s.dir(agent))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var states []State
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || atomicfile.IsTemp(entry.Name()) {
			continue
		}
		if state, err := s.ReadRecord(agent, id); err == nil {
			states = append(states, state)
		}
	}
	return states, nil
}

type sessionLock struct{ file *os.File }

func (s Store) lock(agent, id string) (*sessionLock, error) {
	if err := os.MkdirAll(s.dir(agent), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(s.lockPath(agent, id), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, callError(CodeHeld, "another Agenthail ACP host holds session %s", id)
	}
	return &sessionLock{file: file}, nil
}

func (l *sessionLock) release() {
	if l != nil && l.file != nil {
		_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		l.file.Close()
	}
}

type Line struct {
	TS     time.Time             `json:"ts"`
	Header *Header               `json:"header,omitempty"`
	Item   *surface.TimelineItem `json:"item,omitempty"`
}

type Header struct {
	Identity  string `json:"identity"`
	SessionID string `json:"sessionId"`
	Agent     string `json:"agent"`
}

type transcriptWriter struct {
	mu   sync.Mutex
	file *os.File
}

func (s Store) createTranscript(agent, id string) (*transcriptWriter, error) {
	path := s.TranscriptPath(agent, id)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	writer := &transcriptWriter{file: file}
	if err := writer.write(Line{Header: &Header{Identity: uuid.NewString(), SessionID: id, Agent: agent}}); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	return writer, nil
}

func (s Store) openTranscript(agent, id string) (*transcriptWriter, error) {
	path := s.TranscriptPath(agent, id)
	if err := truncatePartialLine(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrTranscriptGone, path)
		}
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &transcriptWriter{file: file}, nil
}

func truncatePartialLine(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	size := info.Size()
	buffer := make([]byte, 64<<10)
	for end := size; end > 0; {
		start := max(0, end-int64(len(buffer)))
		chunk := buffer[:end-start]
		if _, err := file.ReadAt(chunk, start); err != nil && err != io.EOF {
			return err
		}
		for i := len(chunk) - 1; i >= 0; i-- {
			if chunk[i] == '\n' {
				keep := start + int64(i) + 1
				if keep == size {
					return nil
				}
				return file.Truncate(keep)
			}
		}
		end = start
	}
	return file.Truncate(0)
}

func (w *transcriptWriter) write(line Line) error {
	if line.TS.IsZero() {
		line.TS = time.Now().UTC()
	}
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.file.Write(data)
	return err
}

func (w *transcriptWriter) item(item surface.TimelineItem) error {
	return w.write(Line{Item: &item})
}

func (w *transcriptWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.file.Close()
}
