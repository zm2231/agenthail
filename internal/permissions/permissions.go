// Package permissions reports the macOS privacy grants the Agenthail daemon
// holds. macOS keeps a grant for the process responsible for a request, and
// every process the daemon starts (Claude, its tools, agent-hands) inherits
// the daemon as that process. The package's daemon is the main executable of
// a bundle, so its grants belong to the bundle and survive each update.
//
// launchd makes a job it starts responsible for itself; any other process
// inherits the one that started it. So the grants are the bundle's only when
// launchd runs the daemon as its service.
//
// Only a process the daemon starts can read the grants: the bundle's
// agenthail-permissions executable, which reads them without asking, so no
// prompt shows. A command run from a terminal reports the terminal's grants.
package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/installdir"
)

// Service is the launchd job that runs the daemon.
const Service = "com.agenthail.daemon"

// Daemon is the running daemon: its executable, and the launchd job that
// started it, if any (launchd sets XPC_SERVICE_NAME).
type Daemon struct {
	Executable string
	Service    string
}

// Reporter is the executable beside the daemon in its bundle that prints the
// grants as JSON.
const Reporter = "agenthail-permissions"

// States a grant reports.
const (
	Granted          = "granted"
	Missing          = "missing"
	Denied           = "denied"
	TargetNotRunning = "target_not_running"
	Unknown          = "unknown"
)

// Target is an app the daemon's processes control through Apple Events.
type Target struct {
	ID   string
	Name string
}

// Targets are the apps whose Automation grant the daemon needs. agent-hands
// drives the desktop through Codex Computer Use.
var Targets = []Target{{ID: "com.openai.sky.CUAService", Name: "Codex Computer Use"}}

// Grant is one privacy grant and where in System Settings to change it.
type Grant struct {
	Name       string `json:"name"`
	Target     string `json:"target,omitempty"`
	TargetName string `json:"targetName,omitempty"`
	State      string `json:"state"`
	Pane       string `json:"pane"`
	URL        string `json:"url"`
}

// Report is the daemon's grants. Holder is the bundle macOS keeps them for;
// Problem says why they could not be read.
type Report struct {
	Supported bool    `json:"supported"`
	Holder    string  `json:"holder,omitempty"`
	Grants    []Grant `json:"grants"`
	Problem   string  `json:"problem,omitempty"`
}

const settingsURL = "x-apple.systempreferences:com.apple.preference.security?"

func accessibility(state string) Grant {
	return Grant{Name: "Accessibility", State: state, Pane: "System Settings > Privacy & Security > Accessibility", URL: settingsURL + "Privacy_Accessibility"}
}

func screenRecording(state string) Grant {
	return Grant{Name: "Screen Recording", State: state, Pane: "System Settings > Privacy & Security > Screen & System Audio Recording", URL: settingsURL + "Privacy_ScreenCapture"}
}

func automation(target Target, state string) Grant {
	return Grant{Name: "Automation", Target: target.ID, TargetName: target.Name, State: state, Pane: "System Settings > Privacy & Security > Automation", URL: settingsURL + "Privacy_Automation"}
}

// holder is the bundle macOS keeps the daemon's grants for, or why there is
// none.
func (d Daemon) holder() (string, error) {
	bundle := installdir.Bundle(d.Executable)
	if bundle == "" {
		return "", fmt.Errorf("the daemon runs as %s, outside the Agenthail Daemon bundle, so it holds no grants of its own; install Agenthail from its package", d.Executable)
	}
	if d.Service != Service {
		return "", fmt.Errorf("launchd did not start the daemon as %s, so macOS holds its requests for the process that started it; run 'agenthail daemon install'", Service)
	}
	return bundle, nil
}

// Check reads the daemon's grants. On a system other than macOS there are
// none to read.
func Check(ctx context.Context, daemon Daemon) Report {
	if runtime.GOOS != "darwin" {
		return Report{Grants: []Grant{}}
	}
	report := Report{Supported: true, Grants: []Grant{}}
	holder, err := daemon.holder()
	if err != nil {
		report.Problem = err.Error()
		return report
	}
	report.Holder = holder
	exe := daemon.Executable
	args := make([]string, 0, len(Targets))
	for _, target := range Targets {
		args = append(args, target.ID)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, filepath.Join(filepath.Dir(exe), Reporter), args...).Output()
	if err != nil {
		report.Problem = fmt.Sprintf("read the grants with %s: %s", Reporter, commandError(err))
		return report
	}
	var read struct {
		Accessibility   string            `json:"accessibility"`
		ScreenRecording string            `json:"screenRecording"`
		Automation      map[string]string `json:"automation"`
	}
	if err := json.Unmarshal(out, &read); err != nil {
		report.Problem = fmt.Sprintf("read the grants with %s: %v", Reporter, err)
		return report
	}
	report.Grants = append(report.Grants, accessibility(state(read.Accessibility)), screenRecording(state(read.ScreenRecording)))
	for _, target := range Targets {
		report.Grants = append(report.Grants, automation(target, state(read.Automation[target.ID])))
	}
	return report
}

// Request has macOS ask the person, on the daemon's behalf, for each grant
// not yet decided, which also lists the daemon's bundle in System Settings.
// The prompts wait for the person, so it returns once they are asked; a
// grant they answer shows in the next Check. A grant already denied is not
// asked again; it is changed in System Settings.
func Request(daemon Daemon) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("privacy grants are a macOS setting")
	}
	if _, err := daemon.holder(); err != nil {
		return err
	}
	exe := daemon.Executable
	args := []string{"--ask"}
	for _, target := range Targets {
		args = append(args, target.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	cmd := exec.CommandContext(ctx, filepath.Join(filepath.Dir(exe), Reporter), args...)
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("ask for the grants with %s: %w", Reporter, err)
	}
	go func() {
		_ = cmd.Wait()
		cancel()
	}()
	return nil
}

// askTimeout bounds how long the prompts wait for the person.
const askTimeout = 10 * time.Minute

func state(value string) string {
	switch value {
	case Granted, Missing, Denied, TargetNotRunning:
		return value
	}
	return Unknown
}

func commandError(err error) string {
	if exit, ok := err.(*exec.ExitError); ok && len(strings.TrimSpace(string(exit.Stderr))) > 0 {
		return strings.TrimSpace(string(exit.Stderr))
	}
	return err.Error()
}

// Describe is a grant's state in words, with what to do when it is not
// granted.
func (g Grant) Describe() string {
	subject := g.Name
	if g.TargetName != "" {
		subject = fmt.Sprintf("%s of %s", g.Name, g.TargetName)
	}
	switch g.State {
	case Granted:
		return subject + ": granted"
	case Missing:
		return fmt.Sprintf("%s: missing; run 'agenthail permissions request' to be asked for it, or turn on Agenthail Daemon in %s", subject, g.Pane)
	case Denied:
		return fmt.Sprintf("%s: denied; turn on Agenthail Daemon in %s", subject, g.Pane)
	case TargetNotRunning:
		return fmt.Sprintf("%s: not checked, since %s is not running; it is in %s", subject, g.TargetName, g.Pane)
	}
	return fmt.Sprintf("%s: unknown; check Agenthail Daemon in %s", subject, g.Pane)
}
