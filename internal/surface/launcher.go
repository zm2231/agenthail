package surface

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	LauncherCMUX           = "cmux"
	LauncherTMUX           = "tmux"
	LauncherClaudeBG       = "claude-bg"
	LauncherCodexAppServer = "codex-app-server"
	LauncherClaudeSDK      = "claude-sdk"
	LauncherExternal       = "external"
)

type Location struct {
	Workspace string `json:"workspace,omitempty"`
	Surface   string `json:"surface,omitempty"`
	Session   string `json:"session,omitempty"`
	Pane      string `json:"pane,omitempty"`
}

type Runtime struct {
	Launcher  string    `json:"launcher"`
	Location  *Location `json:"location,omitempty"`
	Focusable bool      `json:"focusable"`
}

type LaunchRequest struct {
	Agent   SurfaceKind
	Cwd     string
	Message string
	Model   string
	Name    string
}

type LaunchResult struct {
	SessionID string
	Location  *Location
	Session   *Session `json:"-"`
}

type Launcher interface {
	ID() string
	Agents() []SurfaceKind
	Available(context.Context) (bool, string)
	Launch(context.Context, LaunchRequest) (LaunchResult, error)
	Locate(context.Context, []Session) map[string]Location
}

type Focuser interface {
	Focus(context.Context, Location) error
}

type LaunchAcceptedError struct {
	Launcher string
	Err      error
}

func (e LaunchAcceptedError) Error() string {
	return fmt.Sprintf("%s launch was accepted but its location could not be resolved: %v", e.Launcher, e.Err)
}

func (e LaunchAcceptedError) Unwrap() error { return e.Err }

type RuntimeTransportUnavailableError struct{ Launcher string }

func (e RuntimeTransportUnavailableError) Error() string {
	return "runtime transport unavailable for launcher " + e.Launcher
}

func ValidateRuntimeTransport(session *Session) error {
	if session != nil && session.Runtime != nil && session.Runtime.Launcher == LauncherClaudeSDK {
		return RuntimeTransportUnavailableError{Launcher: LauncherClaudeSDK}
	}
	return nil
}

type starterLauncher struct {
	id      string
	agents  []SurfaceKind
	starter SessionStarter
}

func (l starterLauncher) ID() string                                            { return l.id }
func (l starterLauncher) Agents() []SurfaceKind                                 { return append([]SurfaceKind(nil), l.agents...) }
func (l starterLauncher) Locate(context.Context, []Session) map[string]Location { return nil }
func (l starterLauncher) Available(context.Context) (bool, string) {
	if l.starter == nil {
		return false, "session starter is not configured"
	}
	return true, "session starter is configured"
}

func (l starterLauncher) Launch(ctx context.Context, request LaunchRequest) (LaunchResult, error) {
	if !contains(l.agents, request.Agent) {
		return LaunchResult{}, fmt.Errorf("launcher %s does not support agent %q", l.id, request.Agent)
	}
	session, _, err := l.starter.StartSession(ctx, SessionStartOptions{
		Cwd: request.Cwd, Message: request.Message, Model: request.Model, Name: request.Name,
	})
	if session == nil || session.ID == "" {
		if err != nil {
			return LaunchResult{}, err
		}
		return LaunchResult{}, errors.New("session starter returned no session id")
	}
	return LaunchResult{SessionID: session.ID, Session: session}, err
}

type unavailableLauncher struct {
	id     string
	agents []SurfaceKind
	detail string
}

func (l unavailableLauncher) ID() string                                            { return l.id }
func (l unavailableLauncher) Agents() []SurfaceKind                                 { return append([]SurfaceKind(nil), l.agents...) }
func (l unavailableLauncher) Locate(context.Context, []Session) map[string]Location { return nil }
func (l unavailableLauncher) Available(context.Context) (bool, string)              { return false, l.detail }
func (l unavailableLauncher) Launch(context.Context, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, fmt.Errorf("launcher %s is unavailable: %s", l.id, l.detail)
}

// NewLaunchers injects existing surface starters into the launcher registry.
// Direct callers that do not select a launcher continue to call StartSession.
func NewLaunchers(surfaces []Surface) []Launcher {
	launchers := []Launcher{newCMUX(), newTMUX()}
	for _, candidate := range surfaces {
		starter, ok := candidate.(SessionStarter)
		if !ok || candidate == nil {
			continue
		}
		switch candidate.Name() {
		case KindClaude:
			launchers = append(launchers, starterLauncher{id: LauncherClaudeBG, agents: []SurfaceKind{KindClaude}, starter: starter})
		case KindCodex:
			launchers = append(launchers, starterLauncher{id: LauncherCodexAppServer, agents: []SurfaceKind{KindCodex}, starter: starter})
		}
	}
	launchers = append(launchers,
		unavailableLauncher{id: LauncherClaudeSDK, agents: []SurfaceKind{KindClaude}, detail: "SDK runtime is not implemented"},
		unavailableLauncher{id: LauncherExternal, detail: "external command policy is not configured"},
	)
	return launchers
}

func contains(values []SurfaceKind, value SurfaceKind) bool {
	for _, candidate := range values {
		if strings.EqualFold(string(candidate), string(value)) {
			return true
		}
	}
	return false
}
