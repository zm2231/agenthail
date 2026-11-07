package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	controlEnv      = "AGENTHAIL_LIVE_CONTROL"
	pairingLifetime = 10 * time.Minute
	// macOS limits a Unix socket path to 104 bytes, and the daemon binds
	// sockets a few levels below the scratch HOME.
	maxRootLength = 60
)

// host is one scratch Agenthail host. Every path it may use lives under root.
type host struct {
	index   int
	name    string
	root    string
	home    string
	port    int
	trap    int
	binary  string
	logPath string

	conversations []conversation
	turn          *liveTurn

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

func newHost(binary, base string, index int, trap int) (*host, error) {
	root, err := os.MkdirTemp(base, "h"+strconv.Itoa(index)+".")
	if err != nil {
		return nil, err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, err
	}
	if len(root) > maxRootLength {
		return nil, fmt.Errorf("scratch directory %s is longer than %d bytes; set TMPDIR to a shorter path", root, maxRootLength)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	h := &host{
		index:   index,
		name:    "Live " + string(rune('A'+index)),
		root:    root,
		home:    filepath.Join(root, "home"),
		port:    port,
		trap:    trap,
		binary:  binary,
		logPath: filepath.Join(root, "daemon.log"),
	}
	for _, dir := range []string{h.home, h.dir("bin"), h.dir("tmp"), h.dir("run"), h.dir("sockets"), filepath.Join(h.home, ".codex")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	return h, nil
}

func (h *host) dir(name string) string { return filepath.Join(h.root, name) }

// daemonTools are the only commands a host finds on its PATH. No agent CLI
// is among them, so a host cannot start the person's Claude or Codex.
var daemonTools = []string{"git", "kill", "ps", "sleep"}

// linkTools puts the daemon's tools in the host's bin directory, which is
// its whole PATH, and returns the executables they resolve to.
func (h *host) linkTools(tools map[string]string) error {
	for name, path := range tools {
		if err := os.Symlink(path, filepath.Join(h.dir("bin"), name)); err != nil {
			return err
		}
	}
	return nil
}

func (h *host) endpoint() string { return "http://127.0.0.1:" + strconv.Itoa(h.port) }

// env is the daemon's whole environment: nothing is inherited, so no setting
// from the person running the harness reaches a scratch host.
func (h *host) env() []string {
	trap := "127.0.0.1:" + strconv.Itoa(h.trap)
	return []string{
		"PATH=" + h.dir("bin"),
		"HOME=" + h.home,
		"CFFIXED_USER_HOME=" + h.home,
		"TMPDIR=" + h.dir("tmp") + "/",
		"XDG_RUNTIME_DIR=" + h.dir("run"),
		"CODEX_HOME=" + filepath.Join(h.home, ".codex"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(h.home, ".claude"),
		"AGENTHAIL_CLAUDE_SOCKET_DIR=" + h.dir("sockets"),
		"AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT=" + strconv.Itoa(h.trap),
		"AGENTHAIL_PUSH_RELAY_URL=http://" + trap,
		"LANG=en_US.UTF-8",
	}
}

// seed writes the registry and files the daemon reads at start: browser
// sign-in off for every consumer, the host's name, the dashboard on its own
// loopback port, and two Claude conversations.
func (h *host) seed() error {
	reg, err := registry.Open(filepath.Join(h.home, ".agenthail", "registry.db"))
	if err != nil {
		return err
	}
	defer reg.Close()
	for _, consumer := range []signin.Consumer{signin.Claude, signin.Notion} {
		if _, err := reg.BindSignin(consumer, signin.RefOff); err != nil {
			return fmt.Errorf("turn off %s sign-in: %w", consumer, err)
		}
	}
	if _, err := reg.SetHostName(strings.ReplaceAll(strings.ToLower(h.name), " ", "-")); err != nil {
		return err
	}
	config, err := json.Marshal(map[string]any{"enabled": true, "listen": "127.0.0.1:" + strconv.Itoa(h.port)})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(h.home, ".agenthail", "dashboard.json"), config, 0o600); err != nil {
		return err
	}
	for i, title := range []string{"Check the release notes", "Tidy the build script"} {
		if err := h.seedConversation(reg, i, title); err != nil {
			return err
		}
	}
	return nil
}

var claudeProjectDirUnsafe = regexp.MustCompile(`[^A-Za-z0-9]`)

func (h *host) seedConversation(reg *registry.Registry, i int, prompt string) error {
	cwd := filepath.Join(h.root, "work", "project-"+strconv.Itoa(i))
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		return err
	}
	if i == 1 {
		if err := h.seedRepository(cwd); err != nil {
			return err
		}
	}
	dir := filepath.Join(h.home, ".claude", "projects", claudeProjectDirUnsafe.ReplaceAllString(cwd, "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	id := uuid.NewString()
	at := time.Now().UTC().Add(time.Duration(i-2) * time.Minute)
	records := []map[string]any{
		{"type": "user", "uuid": uuid.NewString(), "sessionId": id, "cwd": cwd, "timestamp": at.Format(time.RFC3339Nano),
			"message": map[string]any{"role": "user", "content": prompt}},
		{"type": "assistant", "uuid": uuid.NewString(), "sessionId": id, "cwd": cwd, "timestamp": at.Add(time.Second).Format(time.RFC3339Nano),
			"message": map[string]any{"id": "msg-" + id, "role": "assistant", "model": "claude-sonnet-5-5", "content": []map[string]any{{"type": "text", "text": "Done: " + strings.ToLower(prompt) + "."}}}},
	}
	var lines []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		lines = append(append(lines, line...), '\n')
	}
	transcript := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(transcript, lines, 0o600); err != nil {
		return err
	}
	h.conversations = append(h.conversations, conversation{id: id, cwd: cwd, transcript: transcript})
	session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: prompt, Cwd: cwd,
		Status: surface.StatusIdle, Ended: true, Transcript: transcript, LastActive: at.Add(time.Second)}
	fingerprint, err := json.Marshal(session)
	if err != nil {
		return err
	}
	_, _, err = reg.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: string(fingerprint)},
		registry.CatalogEvent{DedupeKey: "seed-" + id, Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)})
	return err
}

// pairingLink mints a pairing in the host's registry and returns the link a
// phone opens, pointing at the host's loopback endpoint. The phone's side of
// pairing, POST /api/v1/pair, runs against the daemon as in production.
func (h *host) pairingLink() (string, error) {
	reg, err := registry.OpenCurrent(filepath.Join(h.home, ".agenthail", "registry.db"))
	if err != nil {
		return "", err
	}
	defer reg.Close()
	pairing, err := reg.CreateDevicePairing("iPhone live test", []string{"read", "control"}, pairingLifetime)
	if err != nil {
		return "", err
	}
	identity, err := reg.HostIdentity()
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Set("endpoint", h.endpoint())
	values.Set("secret", pairing.Secret)
	values.Set("host", identity.ID)
	values.Set("name", identity.Name)
	for _, scope := range pairing.Scopes {
		values.Add("scope", scope)
	}
	return "agenthail://pair?" + values.Encode(), nil
}

func (h *host) running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cmd != nil
}

func (h *host) pid() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cmd == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

// start runs the daemon in the foreground on the host's port and waits until
// it answers.
func (h *host) start(ctx context.Context) error {
	h.mu.Lock()
	if h.cmd != nil {
		h.mu.Unlock()
		return nil
	}
	log, err := os.OpenFile(h.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	cmd := exec.Command(h.binary, "daemon-run")
	cmd.Dir = h.home
	cmd.Env = h.env()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		h.mu.Unlock()
		return err
	}
	done := make(chan struct{})
	h.cmd, h.done = cmd, done
	h.mu.Unlock()
	go func() {
		cmd.Wait()
		log.Close()
		close(done)
	}()

	deadline := time.Now().Add(60 * time.Second)
	for {
		if answered(ctx, h.endpoint()+"/api/v1/pair") {
			return nil
		}
		select {
		case <-done:
			h.clear(cmd)
			return fmt.Errorf("%s exited while starting; see %s", h.name, h.logPath)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not answer on %s within 60s; see %s", h.name, h.endpoint(), h.logPath)
		}
	}
}

func (h *host) clear(cmd *exec.Cmd) {
	h.mu.Lock()
	if h.cmd == cmd {
		h.cmd, h.done = nil, nil
	}
	h.mu.Unlock()
}

// stop signals the daemon the harness started, by its recorded PID, and
// waits for it to exit.
func (h *host) stop() error {
	h.mu.Lock()
	cmd, done := h.cmd, h.done
	h.mu.Unlock()
	if cmd == nil {
		return nil
	}
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		cmd.Process.Kill()
		<-done
	}
	h.clear(cmd)
	return nil
}

func answered(ctx context.Context, address string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// scratchBase is where the hosts' directories go: the system temp directory,
// or /tmp when that is too long for the sockets the daemon binds below it.
// Either must be on the internal disk.
func scratchBase() (string, error) {
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", err
	}
	if len(base) > maxRootLength-len("/h0.0123456789") {
		if base, err = filepath.EvalSymlinks("/tmp"); err != nil {
			return "", err
		}
	}
	if runtime.GOOS == "darwin" && (base == "/Volumes" || strings.HasPrefix(base, "/Volumes/")) {
		return "", fmt.Errorf("TMPDIR %s is on an external volume; use the internal disk", base)
	}
	return base, nil
}

// resolveTools finds each daemon tool on the harness's own PATH. A host may
// run only these executables, the agenthail binary itself, and the helpers
// git runs from its exec path.
func resolveTools(binary string) (links map[string]string, executables map[string]bool, dirs []string, err error) {
	links, executables = map[string]string{}, map[string]bool{}
	self, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, nil, nil, err
	}
	if self, err = filepath.Abs(self); err != nil {
		return nil, nil, nil, err
	}
	executables[self] = true
	for _, name := range daemonTools {
		path, err := exec.LookPath(name)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("the hosts need %s: %w", name, err)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, nil, nil, err
		}
		links[name] = resolved
		executables[resolved] = true
		// On macOS, /usr/bin/git and other developer tools are shims that
		// run the copy inside the selected developer directory.
		if runtime.GOOS == "darwin" {
			if out, err := exec.Command("xcrun", "--find", name).Output(); err == nil {
				if real, err := filepath.EvalSymlinks(strings.TrimSpace(string(out))); err == nil {
					executables[real] = true
				}
			}
		}
	}
	out, err := exec.Command(links["git"], "--exec-path").Output()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("git --exec-path: %w", err)
	}
	gitDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, nil, nil, err
	}
	return links, executables, []string{gitDir}, nil
}
