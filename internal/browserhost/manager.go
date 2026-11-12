package browserhost

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/nodehost"
)

var (
	minRestart = time.Second
	maxRestart = 30 * time.Second
)

type Launch = nodehost.Launch

type Manager struct {
	Home    string
	Launch  func() (Launch, error)
	Changed func()

	once       sync.Once
	supervisor *nodehost.Supervisor
	mu         sync.Mutex
	status     Status
}

func StateDir(home string) string { return filepath.Join(home, ".agenthail", "browser-host") }

func SocketPath(home string) string { return filepath.Join(StateDir(home), "host.sock") }

func BrowsersDir(home string) string { return filepath.Join(StateDir(home), "browsers") }

func (m *Manager) host() *nodehost.Supervisor {
	m.once.Do(func() {
		m.supervisor = &nodehost.Supervisor{
			Name:       "browser host",
			StateDir:   StateDir(m.Home),
			Protocol:   Protocol,
			Launch:     m.Launch,
			Args:       func() []string { return []string{"--browsers", BrowsersDir(m.Home)} },
			Ready:      m.ready,
			Down:       func(err error) { m.setStatus(Status{State: StateUnavailable, Error: err.Error()}) },
			MinRestart: minRestart,
			MaxRestart: maxRestart,
		}
	})
	return m.supervisor
}

func (m *Manager) ready(ctx context.Context, client nodehost.Client) error {
	status, err := Client{client}.Status(ctx)
	if err != nil {
		return err
	}
	m.setStatus(status)
	return nil
}

func (m *Manager) Client() Client { return Client{m.host().Client()} }

func (m *Manager) LogPath() string { return m.host().LogPath() }

func (m *Manager) Run(ctx context.Context) { m.host().Run(ctx) }

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == "" {
		return Status{State: StateUnavailable, Error: "the browser host is not running"}
	}
	return m.status
}

func (m *Manager) Refresh(ctx context.Context) {
	if status, err := m.Client().Status(ctx); err == nil {
		m.setStatus(status)
	}
}

func (m *Manager) setStatus(status Status) {
	m.mu.Lock()
	changed := !reflect.DeepEqual(m.status, status)
	m.status = status
	m.mu.Unlock()
	if changed && m.Changed != nil {
		m.Changed()
	}
}
