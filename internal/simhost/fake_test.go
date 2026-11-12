package simhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSim struct {
	mu       sync.Mutex
	calls    []Command
	devices  map[string]string
	missing  map[string]bool
	describe string
	created  int
	block    map[string]chan struct{}
	onCall   func(Command)
}

const runtimesJSON = `{"runtimes":[{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-18-5","isAvailable":true},{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-27-0","isAvailable":true}]}`

const deviceTypesJSON = `{"devicetypes":[{"identifier":"t.iPad-Pro-13","name":"iPad Pro 13-inch (M5)"},{"identifier":"t.iPad-Pro-11","name":"iPad Pro 11-inch (M5)"},{"identifier":"t.iPhone-17","name":"iPhone 17"}]}`

func newFakeSim() *fakeSim {
	return &fakeSim{devices: map[string]string{}, missing: map[string]bool{}, block: map[string]chan struct{}{}}
}

func (f *fakeSim) exec(_ context.Context, cmd Command) ([]byte, error) {
	f.mu.Lock()
	line0 := strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
	var wait chan struct{}
	for prefix, ch := range f.block {
		if strings.HasPrefix(line0, prefix) {
			wait = ch
		}
	}
	onCall := f.onCall
	f.mu.Unlock()
	if onCall != nil {
		onCall(cmd)
	}
	if wait != nil {
		<-wait
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cmd)
	if f.missing[cmd.Name] {
		return nil, &CommandError{Command: cmd.Name, Err: errors.New("executable file not found")}
	}
	line := strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
	switch {
	case strings.HasPrefix(line, "xcrun simctl list runtimes"):
		return []byte(runtimesJSON), nil
	case strings.HasPrefix(line, "xcrun simctl list devicetypes"):
		return []byte(deviceTypesJSON), nil
	case strings.HasPrefix(line, "xcrun simctl list devices"):
		var rows []string
		for udid, name := range f.devices {
			rows = append(rows, fmt.Sprintf(`{"udid":%q,"name":%q,"state":"Booted"}`, udid, name))
		}
		return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-27-0":[` + strings.Join(rows, ",") + `]}}`), nil
	case strings.HasPrefix(line, "xcrun simctl create"):
		f.created++
		udid := fmt.Sprintf("UDID-%d", f.created)
		f.devices[udid] = cmd.Args[2]
		return []byte(udid + "\n"), nil
	case strings.HasPrefix(line, "xcrun simctl rename"):
		f.devices[cmd.Args[2]] = cmd.Args[3]
	case strings.HasPrefix(line, "axe describe-ui"):
		return []byte(f.describe), nil
	case strings.HasPrefix(line, "axe --version"):
		return []byte("1.8.0\n"), nil
	case strings.HasPrefix(line, "xcrun simctl launch"):
		return []byte(cmd.Args[len(cmd.Args)-1] + ": 4242\n"), nil
	case strings.HasPrefix(line, "xcrun simctl appinfo"):
		return []byte("{\n    CFBundleExecutable = Agenthail;\n}\n"), nil
	case strings.HasPrefix(line, "xcrun simctl spawn"):
		return []byte("line one\nline two\nline three\n"), nil
	}
	return nil, nil
}

func (f *fakeSim) ran(prefix string) []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Command
	for _, cmd := range f.calls {
		if strings.HasPrefix(strings.Join(append([]string{cmd.Name}, cmd.Args...), " "), prefix) {
			out = append(out, cmd)
		}
	}
	return out
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newTestHost(t *testing.T, sim *fakeSim) (*Host, *clock) {
	t.Helper()
	c := &clock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	h := New(Config{Exec: sim.exec, OutRoot: t.TempDir(), LockDir: t.TempDir(), GOOS: "darwin", Now: c.Now})
	t.Cleanup(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, l := range h.leases {
			l.lock.Close()
		}
	})
	return h, c
}

func mustCall(t *testing.T, h *Host, owner string, req Request) map[string]any {
	t.Helper()
	out, err := h.Call(context.Background(), owner, req)
	if err != nil {
		t.Fatalf("%s: %v", req.Action, err)
	}
	return out
}

func refusalCode(t *testing.T, h *Host, owner string, req Request) string {
	t.Helper()
	_, err := h.Call(context.Background(), owner, req)
	var refusal *Error
	if !errors.As(err, &refusal) {
		t.Fatalf("%s = %v, want a refusal", req.Action, err)
	}
	return refusal.Code
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
