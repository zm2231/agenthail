package simhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	IdleLimit  = 10 * time.Minute
	LeaseLimit = 60 * time.Minute
)

type Kind struct {
	Name   string
	Family string
	Prefer string
}

var Kinds = map[string]Kind{
	"iphone": {Name: "Agenthail iPhone", Family: "iPhone", Prefer: "iPhone"},
	"ipad":   {Name: "Agenthail iPad", Family: "iPad", Prefer: "iPad Pro 11-inch"},
}

type Config struct {
	Exec    Exec
	OutRoot string
	LockDir string
	GOOS    string
	Now     func() time.Time
}

type Host struct {
	exec    Exec
	outRoot string
	lockDir string
	goos    string
	now     func() time.Time

	mu     sync.Mutex
	leases map[string]*lease
	create sync.Mutex
}

func New(config Config) *Host {
	h := &Host{exec: config.Exec, outRoot: config.OutRoot, lockDir: config.LockDir, goos: config.GOOS, now: config.Now, leases: map[string]*lease{}}
	if h.exec == nil {
		h.exec = RunCommand
	}
	if h.lockDir == "" {
		h.lockDir = "/tmp"
	}
	if h.goos == "" {
		h.goos = runtime.GOOS
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.outRoot == "" {
		home, _ := os.UserHomeDir()
		h.outRoot = filepath.Join(home, ".agenthail", "simulator", "out")
	}
	return h
}

type Status struct {
	Ready   bool   `json:"ready"`
	Problem string `json:"problem,omitempty"`
	Fix     string `json:"fix,omitempty"`
	AXe     string `json:"axe,omitempty"`
	Runtime string `json:"runtime,omitempty"`
	Simslim bool   `json:"simslim"`
}

func (h *Host) Status(ctx context.Context) Status {
	if h.goos != "darwin" {
		return Status{Problem: "iOS simulators run only on macOS"}
	}
	if _, err := h.run(ctx, "xcrun", "simctl", "help"); err != nil {
		return Status{Problem: "Xcode's simctl is not available", Fix: "install Xcode and run: xcode-select -s /Applications/Xcode.app"}
	}
	runtime, err := h.iosRuntime(ctx)
	if err != nil {
		return Status{Problem: err.Error(), Fix: "install an iOS Simulator runtime in Xcode > Settings > Components"}
	}
	out, err := h.run(ctx, "axe", "--version")
	if err != nil {
		return Status{Runtime: runtime, Problem: "AXe, which taps, types and reads the screen, is not installed", Fix: "brew install cameroncooke/axe/axe"}
	}
	_, slimErr := h.run(ctx, "simslim", "--version")
	return Status{Ready: true, AXe: strings.TrimSpace(string(out)), Runtime: runtime, Simslim: slimErr == nil}
}

func (h *Host) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return h.exec(ctx, Command{Name: name, Args: args})
}

func (h *Host) iosRuntime(ctx context.Context) (string, error) {
	out, err := h.run(ctx, "xcrun", "simctl", "list", "runtimes", "--json")
	if err != nil {
		return "", err
	}
	var list struct {
		Runtimes []struct {
			Identifier  string `json:"identifier"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", fmt.Errorf("read simulator runtimes: %w", err)
	}
	found := ""
	for _, runtime := range list.Runtimes {
		if runtime.IsAvailable && strings.Contains(runtime.Identifier, "iOS") {
			found = runtime.Identifier
		}
	}
	if found == "" {
		return "", fmt.Errorf("no iOS Simulator runtime is installed")
	}
	return found, nil
}

type device struct {
	UDID  string `json:"udid"`
	Name  string `json:"name"`
	State string `json:"state"`
}

func (h *Host) devices(ctx context.Context) ([]device, error) {
	out, err := h.run(ctx, "xcrun", "simctl", "list", "devices", "available", "--json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Devices map[string][]device `json:"devices"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("read simulators: %w", err)
	}
	var all []device
	for _, runtimeDevices := range list.Devices {
		all = append(all, runtimeDevices...)
	}
	return all, nil
}

func (h *Host) find(ctx context.Context, name string) (device, bool, error) {
	all, err := h.devices(ctx)
	if err != nil {
		return device{}, false, err
	}
	var match device
	found := false
	for _, d := range all {
		if d.Name == name && (!found || d.UDID < match.UDID) {
			match, found = d, true
		}
	}
	return match, found, nil
}

// A new device takes its name only once slimmed, so a failed slim is never found and reused.
func (h *Host) resolve(ctx context.Context, kind Kind) (device, error) {
	h.create.Lock()
	defer h.create.Unlock()
	if d, ok, err := h.find(ctx, kind.Name); err != nil || ok {
		return d, err
	}
	runtime, err := h.iosRuntime(ctx)
	if err != nil {
		return device{}, err
	}
	deviceType, err := h.deviceType(ctx, kind)
	if err != nil {
		return device{}, err
	}
	if _, err := h.run(ctx, "simslim", "--version"); err != nil {
		out, err := h.run(ctx, "xcrun", "simctl", "create", kind.Name, deviceType, runtime)
		if err != nil {
			return device{}, err
		}
		return device{UDID: strings.TrimSpace(string(out)), Name: kind.Name, State: "Shutdown"}, nil
	}
	out, err := h.run(ctx, "xcrun", "simctl", "create", kind.Name+" (slimming)", deviceType, runtime)
	if err != nil {
		return device{}, err
	}
	udid := strings.TrimSpace(string(out))
	if err := h.slim(ctx, udid, kind.Name); err != nil {
		_, _ = h.run(ctx, "xcrun", "simctl", "shutdown", udid)
		_, _ = h.run(ctx, "xcrun", "simctl", "delete", udid)
		return device{}, fmt.Errorf("could not slim and name a new %s simulator: %w", kind.Name, err)
	}
	return device{UDID: udid, Name: kind.Name, State: "Shutdown"}, nil
}

func (h *Host) slim(ctx context.Context, udid, name string) error {
	profile, err := os.CreateTemp("", "agenthail-simslim-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(profile.Name())
	if _, err := profile.Write(simslimProfile); err != nil {
		profile.Close()
		return err
	}
	if err := profile.Close(); err != nil {
		return err
	}
	if _, err := h.run(ctx, "simslim", "on", udid, "--profile", profile.Name()); err != nil {
		return err
	}
	if _, err := h.run(ctx, "xcrun", "simctl", "shutdown", udid); err != nil {
		return err
	}
	_, err = h.run(ctx, "xcrun", "simctl", "rename", udid, name)
	return err
}

func (h *Host) deviceType(ctx context.Context, kind Kind) (string, error) {
	out, err := h.run(ctx, "xcrun", "simctl", "list", "devicetypes", "--json")
	if err != nil {
		return "", err
	}
	var list struct {
		DeviceTypes []struct {
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		} `json:"devicetypes"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", fmt.Errorf("read simulator device types: %w", err)
	}
	for _, prefix := range []string{kind.Prefer, kind.Family} {
		for _, t := range list.DeviceTypes {
			if strings.HasPrefix(t.Name, prefix) {
				return t.Identifier, nil
			}
		}
	}
	return "", fmt.Errorf("Xcode has no %s simulator device type", kind.Family)
}
