// Package triggers runs an operator's configured command, or touches a
// file, when a session on this host finishes a turn.
package triggers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	OutcomeFinished = "finished"
	OutcomeFailed   = "failed"

	DefaultTimeout = 60 * time.Second
	MaxTimeout     = 600 * time.Second
	maxConfigBytes = 1 << 20
)

var (
	namePattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	envPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	outcomes    = []string{OutcomeFinished, OutcomeFailed}
	surfaces    = []string{"codex", "claude", "notion"}
)

type Config struct {
	Triggers []Trigger
}

type Trigger struct {
	Name      string
	Run       []string
	Touch     string
	When      When
	Delegated bool
	Timeout   time.Duration
	Env       map[string]string
}

type When struct {
	Outcome []string `json:"outcome,omitempty"`
	Project []string `json:"project,omitempty"`
	Surface []string `json:"surface,omitempty"`
	Session []string `json:"session,omitempty"`
}

type configFile struct {
	Version  int           `json:"version"`
	Triggers []triggerFile `json:"triggers"`
}

type triggerFile struct {
	Name           string            `json:"name"`
	Run            []string          `json:"run,omitempty"`
	Touch          string            `json:"touch,omitempty"`
	When           When              `json:"when"`
	Delegated      bool              `json:"delegated,omitempty"`
	TimeoutSeconds *int              `json:"timeoutSeconds,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

// Load reads the trigger file. A missing file is no triggers; any other
// problem is an error and no trigger from the file is used.
func Load(path string) (Config, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	if !info.Mode().IsRegular() {
		return Config{}, fmt.Errorf("%s is not a regular file", path)
	}
	if err := ownedPrivately(path, info); err != nil {
		return Config{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return Config{}, err
	}
	if len(data) > maxConfigBytes {
		return Config{}, fmt.Errorf("%s is larger than %d bytes", path, maxConfigBytes)
	}
	return Parse(data)
}

func ownedPrivately(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is mode %04o; make it 0600", path, info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %s", path)
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to uid %d, not the daemon's uid %d", path, stat.Uid, os.Getuid())
	}
	return nil
}

func Parse(data []byte) (Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file configFile
	if err := decoder.Decode(&file); err != nil {
		return Config{}, fmt.Errorf("parse triggers: %w", err)
	}
	if decoder.More() {
		return Config{}, errors.New("parse triggers: more than one JSON document")
	}
	if file.Version != 1 {
		return Config{}, fmt.Errorf("triggers version %d is not supported; use 1", file.Version)
	}
	config := Config{Triggers: make([]Trigger, 0, len(file.Triggers))}
	seen := map[string]bool{}
	for i, entry := range file.Triggers {
		trigger, err := entry.validate()
		if err != nil {
			label := entry.Name
			if label == "" {
				label = fmt.Sprintf("#%d", i+1)
			}
			return Config{}, fmt.Errorf("trigger %s: %w", label, err)
		}
		if seen[trigger.Name] {
			return Config{}, fmt.Errorf("trigger %s is defined twice", trigger.Name)
		}
		seen[trigger.Name] = true
		config.Triggers = append(config.Triggers, trigger)
	}
	return config, nil
}

func (f triggerFile) validate() (Trigger, error) {
	if !namePattern.MatchString(f.Name) {
		return Trigger{}, errors.New("name must be 1 to 64 characters of a-z, 0-9 and -")
	}
	if (len(f.Run) > 0) == (f.Touch != "") {
		return Trigger{}, errors.New("set exactly one of run and touch")
	}
	trigger := Trigger{Name: f.Name, Run: f.Run, Touch: f.Touch, When: f.When, Delegated: f.Delegated, Timeout: DefaultTimeout, Env: f.Env}
	if len(f.Run) > 0 {
		if err := safeExecutable(f.Run[0]); err != nil {
			return Trigger{}, err
		}
	} else if !filepath.IsAbs(f.Touch) || filepath.Clean(f.Touch) != f.Touch {
		return Trigger{}, fmt.Errorf("touch must be a clean absolute path, not %q", f.Touch)
	}
	if f.TimeoutSeconds != nil {
		seconds := time.Duration(*f.TimeoutSeconds) * time.Second
		if seconds < time.Second || seconds > MaxTimeout {
			return Trigger{}, fmt.Errorf("timeoutSeconds must be 1 to %d", int(MaxTimeout.Seconds()))
		}
		trigger.Timeout = seconds
	}
	for name := range f.Env {
		if !envPattern.MatchString(name) || strings.HasPrefix(name, "AGENTHAIL_") {
			return Trigger{}, fmt.Errorf("env name %q is not allowed", name)
		}
	}
	for _, value := range f.When.Outcome {
		if !slices.Contains(outcomes, value) {
			return Trigger{}, fmt.Errorf("when.outcome %q is not one of %s", value, strings.Join(outcomes, ", "))
		}
	}
	for _, value := range f.When.Surface {
		if !slices.Contains(surfaces, value) {
			return Trigger{}, fmt.Errorf("when.surface %q is not one of %s", value, strings.Join(surfaces, ", "))
		}
	}
	for _, list := range [][]string{f.When.Project, f.When.Session} {
		for _, value := range list {
			if strings.TrimSpace(value) == "" {
				return Trigger{}, errors.New("when lists cannot hold an empty entry")
			}
		}
	}
	return trigger, nil
}

func safeExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("run[0] must be an absolute path, not %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("run[0]: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("run[0] %s is not an executable file", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("run[0] %s is writable by its group or others", path)
	}
	return nil
}

func (t Trigger) Matches(c Completion) bool {
	if c.Delegated && !t.Delegated {
		return false
	}
	if len(t.When.Outcome) > 0 && !slices.Contains(t.When.Outcome, c.Outcome) {
		return false
	}
	if len(t.When.Surface) > 0 && !slices.Contains(t.When.Surface, c.Surface) {
		return false
	}
	if len(t.When.Project) > 0 && !slices.Contains(t.When.Project, c.ProjectID) {
		return false
	}
	if len(t.When.Session) > 0 && !slices.ContainsFunc(t.When.Session, func(entry string) bool {
		alias, isAlias := strings.CutPrefix(entry, "@")
		return entry == c.SessionID || c.Alias != "" && (isAlias && alias == c.Alias || entry == c.Alias)
	}) {
		return false
	}
	return true
}
