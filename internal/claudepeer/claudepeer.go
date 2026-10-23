package claudepeer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	DefaultSocketDir    = "/tmp/cc-socks"
	PeerProtocolVersion = 1
	defaultSocketDir    = DefaultSocketDir
	peerProtocol        = PeerProtocolVersion
	maxLineBytes        = 64 << 10
	maxFrames           = 16
	readDeadline        = 5 * time.Second
	firstLineDeadline   = 3 * time.Second
	noticeDeadline      = time.Second
)

var heartbeatInterval = 2 * time.Second

type Config struct {
	Home         string
	RegistryPath string
	Session      surface.Session
	SocketDir    string
	ControlPath  string
	ManifestPath string
	Generation   string
	ProcessToken string
	ReplyRelay   bool
	BusyDelivery string
}

// PeerName is the name Claude lists for an Agenthail peer.
func PeerName(kind surface.SurfaceKind, name string) string {
	return "agenthail/" + string(kind) + ": " + name
}

// replyTarget is how agenthail send on this host reaches the session, empty
// when it has no alias.
func replyTarget(reg *registry.Registry, id string) string {
	target, _ := reg.ReplyTarget(id)
	return target
}

// listedName is the name Claude lists for the session: its reply target,
// else its title.
func listedName(reg *registry.Registry, session surface.Session) string {
	if target := replyTarget(reg, session.ID); target != "" {
		return target
	}
	return session.Name
}

type Ready struct {
	PID         int    `json:"pid"`
	SocketPath  string `json:"socketPath"`
	ControlPath string `json:"controlPath"`
}

type OwnershipManifest struct {
	Agenthail     string `json:"agenthail"`
	State         string `json:"state"`
	Generation    string `json:"generation"`
	SourceID      string `json:"sourceSessionId"`
	ProcessToken  string `json:"processToken"`
	PID           int    `json:"pid"`
	ProcStart     string `json:"procStart"`
	ControlPath   string `json:"controlPath"`
	ControlDevice uint64 `json:"controlDevice"`
	ControlInode  uint64 `json:"controlInode"`
	SocketPath    string `json:"socketPath"`
	SocketDevice  uint64 `json:"socketDevice"`
	SocketInode   uint64 `json:"socketInode"`
	RecordPath    string `json:"recordPath"`
}

type sessionRecord struct {
	PID                 int    `json:"pid"`
	SessionID           string `json:"sessionId"`
	Cwd                 string `json:"cwd"`
	StartedAt           int64  `json:"startedAt"`
	ProcStart           string `json:"procStart"`
	Version             string `json:"version"`
	PeerProtocol        int    `json:"peerProtocol"`
	Kind                string `json:"kind"`
	Entrypoint          string `json:"entrypoint"`
	MessagingSocketPath string `json:"messagingSocketPath"`
	Name                string `json:"name"`
	NameSince           int64  `json:"nameSince"`
	Status              string `json:"status"`
	UpdatedAt           int64  `json:"updatedAt"`
	StatusUpdatedAt     int64  `json:"statusUpdatedAt"`
	Agenthail           string `json:"agenthail"`
	ProcessToken        string `json:"processToken"`
}

type frame struct {
	MsgV  int    `json:"msgV"`
	MsgID string `json:"msg_id"`
	// UUID is the id Claude gives the message and names in its hold reports.
	UUID     string          `json:"uuid,omitempty"`
	Type     string          `json:"type"`
	Message  json.RawMessage `json:"message"`
	Priority string          `json:"priority,omitempty"`
	From     string          `json:"from"`
}

type messageBody struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type controlRequest struct {
	TargetSocket string `json:"targetSocket"`
	SenderID     string `json:"senderId"`
	Message      string `json:"message"`
	ReplySocket  string `json:"replySocket,omitempty"`
	MsgID        string `json:"msgId,omitempty"`
	PersonName   string `json:"personName,omitempty"`
}

type controlResponse struct {
	OK     bool                `json:"ok"`
	Result *surface.SendResult `json:"result,omitempty"`
	Error  *controlError       `json:"error,omitempty"`
}

type controlError struct {
	Class string `json:"class"`
	Kind  string `json:"kind,omitempty"`
	Error string `json:"error"`
}

func RuntimeRoot(home string) string {
	return filepath.Join(home, ".agenthail", "run", "p")
}

func WorkerControlPath(home, generation string, pid int) string {
	return filepath.Join(RuntimeRoot(home), generation, strconv.Itoa(pid)+".sock")
}

func WorkerManifestPath(home, generation string, pid int) string {
	return filepath.Join(RuntimeRoot(home), generation, strconv.Itoa(pid)+".json")
}

func RunWorker(ctx context.Context, config Config, parent io.Reader, ready io.Writer) error {
	c, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	if c.ProcessToken != "" && os.Getenv("AGENTHAIL_PEER_TOKEN") != c.ProcessToken {
		return errors.New("Claude peer worker process token does not match its launch environment")
	}
	if c.ReplyRelay {
		return runReplyRelay(ctx, c, parent, ready)
	}
	pid := os.Getpid()
	procStart, err := ProcessStart(pid)
	if err != nil {
		return fmt.Errorf("read process start: %w", err)
	}
	reg, err := registry.Open(c.RegistryPath)
	if err != nil {
		return err
	}
	defer reg.Close()
	claudeID := surface.PeerUUID(c.Session.Surface, c.Session.ID)
	socketPath := filepath.Join(c.SocketDir, strconv.Itoa(pid)+".sock")
	controlPath := c.ControlPath
	if controlPath == "" {
		controlPath = WorkerControlPath(c.Home, c.Generation, pid)
	}
	manifestPath := c.ManifestPath
	if manifestPath == "" {
		manifestPath = WorkerManifestPath(c.Home, c.Generation, pid)
	}
	if err := os.MkdirAll(c.SocketDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(controlPath), 0700); err != nil {
		return err
	}
	recordPath := filepath.Join(surface.ClaudeConfigDir(c.Home), "sessions", strconv.Itoa(pid)+".json")
	if err := requireAbsent(socketPath, controlPath, recordPath, manifestPath); err != nil {
		return err
	}
	pending := OwnershipManifest{Agenthail: "peer-worker", State: "starting", Generation: c.Generation, SourceID: c.Session.ID, PID: pid, ProcStart: procStart, ControlPath: controlPath, SocketPath: socketPath, RecordPath: recordPath}
	pending.ProcessToken = c.ProcessToken
	if err := writeExclusiveJSONAtomic(manifestPath, pending); err != nil {
		return fmt.Errorf("write pending peer ownership manifest: %w", err)
	}
	ln, err := listenOwned(socketPath)
	if err != nil {
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("listen peer socket: %w", err)
	}
	socketDevice, socketInode, err := socketIdentity(socketPath)
	if err != nil {
		ln.Close()
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("read peer socket identity: %w", err)
	}
	peerOwned := pending
	peerOwned.SocketDevice = socketDevice
	peerOwned.SocketInode = socketInode
	if err := replaceManifest(manifestPath, pending, peerOwned); err != nil {
		ln.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("record peer socket ownership: %w", err)
	}
	pending = peerOwned
	cl, err := listenOwned(controlPath)
	if err != nil {
		ln.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("listen control socket: %w", err)
	}
	started := time.Now().UnixMilli()
	record := sessionRecord{PID: pid, SessionID: claudeID, Cwd: c.Session.Cwd, StartedAt: started, ProcStart: procStart,
		Version: "agenthail", PeerProtocol: peerProtocol, Kind: "interactive", Entrypoint: "cli", MessagingSocketPath: socketPath,
		Name: PeerName(c.Session.Surface, listedName(reg, c.Session)), NameSince: started, Status: status(c.Session), UpdatedAt: started, StatusUpdatedAt: started, Agenthail: "peer-worker", ProcessToken: c.ProcessToken}
	controlDevice, controlInode, err := socketIdentity(controlPath)
	if err != nil {
		ln.Close()
		cl.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		return fmt.Errorf("read control socket identity: %w", err)
	}
	controlOwned := pending
	controlOwned.ControlDevice = controlDevice
	controlOwned.ControlInode = controlInode
	if err := replaceManifest(manifestPath, pending, controlOwned); err != nil {
		ln.Close()
		cl.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		removeOwnedSocket(controlPath, controlDevice, controlInode)
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("record control socket ownership: %w", err)
	}
	pending = controlOwned
	manifest := pending
	manifest.State = "ready"
	if err := replaceManifest(manifestPath, pending, manifest); err != nil {
		ln.Close()
		cl.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		removeOwnedSocket(controlPath, controlDevice, controlInode)
		cleanupManifest(manifestPath, pending)
		return fmt.Errorf("complete peer ownership manifest: %w", err)
	}
	if err := writeExclusiveJSONAtomic(recordPath, record); err != nil {
		ln.Close()
		cl.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		removeOwnedSocket(controlPath, controlDevice, controlInode)
		cleanupManifest(manifestPath, manifest)
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var listeners, handlers sync.WaitGroup
	listeners.Add(3)
	go func() {
		defer listeners.Done()
		heartbeat(ctx.Done(), recordPath, record, reg, c.Session.ID)
	}()
	go func() { defer listeners.Done(); acceptPeers(&handlers, ln, reg, c, socketPath) }()
	go func() { defer listeners.Done(); acceptControls(&handlers, cl, reg, c, socketPath) }()
	defer func() {
		cancel()
		ln.Close()
		cl.Close()
		listeners.Wait()
		handlers.Wait()
		cleanupOwned(recordPath, record)
		cleanupManifest(manifestPath, manifest)
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		removeOwnedSocket(controlPath, controlDevice, controlInode)
	}()
	if ready != nil {
		if err := json.NewEncoder(ready).Encode(Ready{PID: pid, SocketPath: socketPath, ControlPath: controlPath}); err != nil {
			return err
		}
	}
	parentDone := make(chan struct{})
	if parent != nil {
		go watchParent(parent, parentDone)
	}
	select {
	case <-ctx.Done():
	case <-parentDone:
	}
	return nil
}

func Send(ctx context.Context, controlPath, senderID, targetSocket, message string) (*surface.SendResult, error) {
	return SendWithReplySocket(ctx, controlPath, senderID, targetSocket, "", message)
}

func SendWithReplySocket(ctx context.Context, controlPath, senderID, targetSocket, replySocket, message string) (*surface.SendResult, error) {
	if err := ValidateSend(senderID, targetSocket, message); err != nil {
		return nil, err
	}
	conn, err := dialContext(ctx, controlPath)
	if err != nil {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("peer control socket: %w", err))
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(readDeadline)); err != nil {
		return nil, err
	}
	req, _ := json.Marshal(controlRequest{TargetSocket: targetSocket, SenderID: senderID, Message: message, ReplySocket: replySocket, MsgID: surface.MessageUUID(ctx), PersonName: surface.PersonName(ctx)})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	var resp controlResponse
	if err := json.NewDecoder(io.LimitReader(conn, maxLineBytes)).Decode(&resp); err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	if resp.OK && resp.Result != nil {
		return resp.Result, nil
	}
	if resp.Error == nil {
		return nil, surface.DeliveryOutcomeUnknown(errors.New("malformed peer control response"))
	}
	switch resp.Error.Class {
	case "terminal":
		return nil, surface.DeliveryTerminal(errors.New(resp.Error.Error), surface.DeliveryTerminalKind(resp.Error.Kind))
	case "unavailable":
		return nil, surface.DeliveryUnavailable(errors.New(resp.Error.Error))
	default:
		return nil, surface.DeliveryOutcomeUnknown(errors.New(resp.Error.Error))
	}
}

// SendNative sends as a Claude session through its own socket. fromMode is the
// sender's permission class as its runtime reports it, or "" when unknown:
// Claude holds a message to a session that bypasses permission prompts unless
// its sender states that it does too.
func SendNative(ctx context.Context, home string, sender surface.Session, senderSocket, targetSocket, fromMode, message string) (*surface.SendResult, error) {
	if err := ValidateSend(sender.ID, targetSocket, message); err != nil {
		return nil, err
	}
	if err := validateTargetSocket(defaultSocketDir, targetSocket); err != nil {
		return nil, err
	}
	id := messageID(surface.MessageUUID(ctx))
	content := peerContent(sender, surface.ReplyTarget(ctx), senderSocket, fromMode, message)
	payload, _ := json.Marshal(frame{MsgV: peerProtocol, MsgID: id, UUID: id, Type: "user", Message: mustJSON(messageBody{Role: "user", Content: content}), Priority: "next", From: "uds:" + senderSocket})
	if len(payload) >= maxLineBytes {
		return nil, surface.DeliveryTerminal(errors.New("Claude peer message exceeds the 64 KiB frame limit"), surface.DeliveryInvalidRequest)
	}
	if err := sendAuthenticatedFrame(home, targetSocket, payload); err != nil {
		return nil, err
	}
	return &surface.SendResult{UUID: id, Accepted: true}, nil
}

func ValidateSend(senderID, targetSocket, message string) error {
	if strings.TrimSpace(senderID) == "" || strings.TrimSpace(targetSocket) == "" || strings.TrimSpace(message) == "" {
		return surface.DeliveryTerminal(errors.New("sender, target socket, and message are required"), surface.DeliveryInvalidRequest)
	}
	req, err := json.Marshal(controlRequest{TargetSocket: targetSocket, SenderID: senderID, Message: message})
	if err != nil {
		return surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	if len(req) >= maxLineBytes {
		return surface.DeliveryTerminal(errors.New("Claude peer message exceeds the 64 KiB frame limit"), surface.DeliveryInvalidRequest)
	}
	return nil
}

func runReplyRelay(ctx context.Context, c Config, parent io.Reader, ready io.Writer) error {
	pid := os.Getpid()
	procStart, err := ProcessStart(pid)
	if err != nil {
		return fmt.Errorf("read relay process start: %w", err)
	}
	socketPath := filepath.Join(c.SocketDir, strconv.Itoa(pid)+".sock")
	manifestPath := c.ManifestPath
	if manifestPath == "" {
		manifestPath = WorkerManifestPath(c.Home, c.Generation, pid)
	}
	if err := os.MkdirAll(c.SocketDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		return err
	}
	if err := requireAbsent(socketPath, manifestPath); err != nil {
		return err
	}
	manifest := OwnershipManifest{Agenthail: "peer-relay", State: "starting", Generation: c.Generation, SourceID: c.Session.ID, ProcessToken: c.ProcessToken, PID: pid, ProcStart: procStart, SocketPath: socketPath}
	if err := writeExclusiveJSONAtomic(manifestPath, manifest); err != nil {
		return err
	}
	ln, err := listenOwned(socketPath)
	if err != nil {
		cleanupManifest(manifestPath, manifest)
		return err
	}
	socketDevice, socketInode, err := socketIdentity(socketPath)
	if err != nil {
		ln.Close()
		cleanupManifest(manifestPath, manifest)
		return err
	}
	manifest.SocketDevice, manifest.SocketInode, manifest.State = socketDevice, socketInode, "ready"
	if err := replaceManifest(manifestPath, OwnershipManifest{Agenthail: "peer-relay", State: "starting", Generation: c.Generation, SourceID: c.Session.ID, ProcessToken: c.ProcessToken, PID: pid, ProcStart: procStart, SocketPath: socketPath}, manifest); err != nil {
		ln.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		cleanupManifest(manifestPath, manifest)
		return err
	}
	reg, err := registry.Open(c.RegistryPath)
	if err != nil {
		ln.Close()
		removeOwnedSocket(socketPath, socketDevice, socketInode)
		cleanupManifest(manifestPath, manifest)
		return err
	}
	defer reg.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var handlers sync.WaitGroup
	go acceptRelays(&handlers, ln, reg, c, socketPath)
	if ready != nil {
		if err := json.NewEncoder(ready).Encode(Ready{PID: pid, SocketPath: socketPath}); err != nil {
			return err
		}
	}
	parentDone := make(chan struct{})
	if parent != nil {
		go watchParent(parent, parentDone)
	}
	defer func() {
		cancel()
		ln.Close()
		handlers.Wait()
		cleanupManifest(manifestPath, manifest)
		removeOwnedSocket(socketPath, socketDevice, socketInode)
	}()
	select {
	case <-ctx.Done():
	case <-parentDone:
	}
	return nil
}

func normalizeConfig(c Config) (Config, error) {
	if c.Home == "" {
		c.Home, _ = os.UserHomeDir()
	}
	if c.Home == "" || c.Session.ID == "" || c.Session.Surface == "" {
		return c, errors.New("home, session id, and session surface are required")
	}
	if c.SocketDir == "" {
		c.SocketDir = defaultSocketDir
	}
	if c.Generation == "" {
		c.Generation = "direct"
	}
	return c, nil
}

func status(s surface.Session) string {
	if s.Status == surface.StatusBusy {
		return "busy"
	}
	if s.Status == surface.StatusIdle {
		return "idle"
	}
	return "waiting"
}

// ProcessStart is the process's start time as ps reports it in UTC, which
// tells a process from a later one that reuses its pid.
func ProcessStart(pid int) (string, error) {
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := command.Output()
	return strings.TrimSpace(string(out)), err
}

func listenOwned(path string) (net.Listener, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		os.Remove(path)
		return nil, err
	}
	if unixListener, ok := ln.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	return ln, nil
}

func requireAbsent(paths ...string) error {
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("path already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func writeExclusiveJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create owned session record: %w", err)
	}
	encodeErr := json.NewEncoder(f).Encode(value)
	syncErr := f.Sync()
	closeErr := f.Close()
	if encodeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(encodeErr, syncErr, closeErr)
	}
	return nil
}

func writeExclusiveJSONAtomic(path string, value any) error {
	tmp := path + ".tmp." + uuid.NewString()
	if err := writeExclusiveJSON(tmp, value); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, path); err != nil {
		return fmt.Errorf("publish owned record: %w", err)
	}
	return nil
}

func replaceManifest(path string, current, replacement OwnershipManifest) error {
	if !manifestOwned(path, current) {
		return fmt.Errorf("peer ownership manifest changed before activation")
	}
	tmp := path + ".tmp." + uuid.NewString()
	if err := writeExclusiveJSON(tmp, replacement); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if !manifestOwned(path, current) {
		return fmt.Errorf("peer ownership manifest changed before activation")
	}
	return os.Rename(tmp, path)
}

func heartbeat(stop <-chan struct{}, path string, record sessionRecord, reg *registry.Registry, id string) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			next := record
			if session, err := reg.Session(id); err == nil {
				next.Name = PeerName(session.Surface, listedName(reg, *session))
				next.Cwd = session.Cwd
				next.Status = status(*session)
			}
			_, statErr := os.Lstat(path)
			missing := os.IsNotExist(statErr)
			// Claude Code rewrites its own records only when they change, so an
			// unchanged peer record stays put instead of being rewritten each tick.
			if !missing && next.Name == record.Name && next.Cwd == record.Cwd && next.Status == record.Status {
				continue
			}
			next.UpdatedAt = now.UnixMilli()
			next.StatusUpdatedAt = next.UpdatedAt
			if missing {
				if writeExclusiveJSON(path, next) == nil {
					record = next
				}
				continue
			}
			tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
			f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				continue
			}
			encodeErr := json.NewEncoder(f).Encode(next)
			closeErr := f.Close()
			if encodeErr == nil && closeErr == nil && recordOwned(path, next) && os.Rename(tmp, path) == nil {
				record = next
			}
			_ = os.Remove(tmp)
		}
	}
}

func watchParent(parent io.Reader, done chan<- struct{}) {
	_, _ = io.Copy(io.Discard, parent)
	close(done)
}

func recordOwned(path string, record sessionRecord) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var current sessionRecord
	return json.Unmarshal(data, &current) == nil && current.Agenthail == record.Agenthail && current.PID == record.PID && current.SessionID == record.SessionID && current.ProcStart == record.ProcStart && current.ProcessToken == record.ProcessToken
}

func cleanupOwned(path string, record sessionRecord) {
	if recordOwned(path, record) {
		_ = os.Remove(path)
	}
}

func manifestOwned(path string, expected OwnershipManifest) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var current OwnershipManifest
	return json.Unmarshal(data, &current) == nil && current == expected
}

func cleanupManifest(path string, manifest OwnershipManifest) {
	if manifestOwned(path, manifest) {
		_ = os.Remove(path)
	}
}

// Reconcile retires manifest-backed orphan workers and removes dead, same-user
// Agenthail artifacts. Foreign or ambiguous endpoints are preserved.
func Reconcile(home, socketDir string) error {
	if socketDir == "" {
		socketDir = defaultSocketDir
	}
	root := RuntimeRoot(home)
	entries, err := filepath.Glob(filepath.Join(root, "*", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range entries {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var manifest OwnershipManifest
		if json.Unmarshal(data, &manifest) != nil || !validManifest(root, socketDir, path, manifest) {
			continue
		}
		if processOwnsManifest(manifest) && processIsPeerWorker(manifest.PID) {
			_ = syscall.Kill(manifest.PID, syscall.SIGTERM)
			deadline := time.Now().Add(time.Second)
			for processOwnsManifest(manifest) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if processOwnsManifest(manifest) {
				_ = syscall.Kill(manifest.PID, syscall.SIGKILL)
			}
		} else if processExists(manifest.PID) {
			continue
		}
		if processExists(manifest.PID) {
			continue
		}
		removeManifestSocket(manifest.ControlPath, manifest.ControlDevice, manifest.ControlInode)
		removeManifestSocket(manifest.SocketPath, manifest.SocketDevice, manifest.SocketInode)
		removeOwnedRecord(manifest.RecordPath, manifest)
		cleanupManifest(path, manifest)
		_ = os.Remove(filepath.Dir(path))
	}
	return nil
}

func validManifest(root, socketDir, path string, manifest OwnershipManifest) bool {
	if (manifest.Agenthail != "peer-worker" && manifest.Agenthail != "peer-relay") || manifest.PID <= 0 || manifest.Generation == "" || manifest.ProcStart == "" || (manifest.State != "starting" && manifest.State != "ready") {
		return false
	}
	if (manifest.ControlDevice == 0) != (manifest.ControlInode == 0) || (manifest.SocketDevice == 0) != (manifest.SocketInode == 0) {
		return false
	}
	wantDir := filepath.Join(root, manifest.Generation)
	if filepath.Dir(path) != wantDir || manifest.SocketPath != filepath.Join(socketDir, strconv.Itoa(manifest.PID)+".sock") {
		return false
	}
	if manifest.Agenthail == "peer-relay" {
		return manifest.State == "ready" && manifest.ControlPath == "" && manifest.ControlDevice == 0 && manifest.ControlInode == 0 && manifest.RecordPath == "" && manifest.SocketInode != 0
	}
	if manifest.State == "ready" && (manifest.ControlInode == 0 || manifest.SocketInode == 0) {
		return false
	}
	if manifest.ControlPath != filepath.Join(wantDir, strconv.Itoa(manifest.PID)+".sock") {
		return false
	}
	home := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	return manifest.RecordPath == filepath.Join(surface.ClaudeConfigDir(home), "sessions", strconv.Itoa(manifest.PID)+".json")
}

// On Linux Claude Code records procStart as the starttime field of
// /proc/<pid>/stat, in clock ticks since boot.
func IsClockTicks(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func ProcStartTicks(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	return procStatStartTime(string(data))
}

func procStatStartTime(stat string) (string, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("malformed /proc stat")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return "", fmt.Errorf("malformed /proc stat")
	}
	return fields[19], nil
}

// nativeProcessMatches reports whether pid is the process a native Claude
// record names, by the start time Claude recorded for it.
func nativeProcessMatches(pid int, recorded string) bool {
	if IsClockTicks(recorded) {
		actual, err := ProcStartTicks(pid)
		return err == nil && actual == recorded
	}
	return processMatches(pid, recorded)
}

func processMatches(pid int, expected string) bool {
	actual, err := ProcessStart(pid)
	return err == nil && actual == expected
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func processOwnsManifest(manifest OwnershipManifest) bool {
	if manifest.ProcessToken == "" || !processMatches(manifest.PID, manifest.ProcStart) {
		return false
	}
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(manifest.PID), "-o", "command=").Output()
	if err != nil {
		return false
	}
	wantToken := manifest.ProcessToken
	foundWorker := false
	foundToken := false
	for _, field := range strings.Fields(string(out)) {
		foundWorker = foundWorker || field == "claude-peer-worker" || field == "claude-peer-relay"
		foundToken = foundToken || field == wantToken
	}
	return foundWorker && foundToken
}

func processIsPeerWorker(pid int) bool {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && (strings.Contains(string(out), "agenthail claude-peer-worker") || strings.Contains(string(out), "agenthail claude-peer-relay"))
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func socketIdentity(path string) (uint64, uint64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 {
		return 0, 0, fmt.Errorf("not a Unix socket: %s", path)
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func removeOwnedSocket(path string, device, inode uint64) {
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && info.Mode()&os.ModeSocket != 0 && ownedByCurrentUser(info) && uint64(stat.Dev) == device && uint64(stat.Ino) == inode {
		_ = os.Remove(path)
	}
}

func removeManifestSocket(path string, device, inode uint64) bool {
	if device == 0 || inode == 0 {
		_, err := os.Lstat(path)
		return os.IsNotExist(err)
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || !ownedByCurrentUser(info) || uint64(stat.Dev) != device || uint64(stat.Ino) != inode {
		return false
	}
	if !SocketRefusesConnections(path) {
		return false
	}
	removeErr := os.Remove(path)
	return removeErr == nil || os.IsNotExist(removeErr)
}

var dialSocket = func(path string) (net.Conn, error) {
	return net.DialTimeout("unix", path, 200*time.Millisecond)
}

// SocketRefusesConnections reports whether nothing listens at path. A
// filesystem can hand a freed inode to the next socket made at the same path,
// so a matching device and inode do not prove a socket is stale; only a
// refused connection does, and any other dial result keeps the socket.
func SocketRefusesConnections(path string) bool {
	connection, err := dialSocket(path)
	if err == nil {
		connection.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

func removeOwnedRecord(path string, manifest OwnershipManifest) {
	if !ownedRecordMatches(path, manifest) {
		return
	}
	_ = os.Remove(path)
}

func ownedRecordMatches(path string, manifest OwnershipManifest) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var record sessionRecord
	return json.Unmarshal(data, &record) == nil && record.Agenthail == "peer-worker" && record.PID == manifest.PID && record.ProcStart == manifest.ProcStart && record.ProcessToken == manifest.ProcessToken
}

func acceptRelays(wg *sync.WaitGroup, ln net.Listener, reg *registry.Registry, c Config, ownSocket string) {
	sem := make(chan struct{}, 8)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-sem }(); handleRelay(conn, reg, c, ownSocket) }()
	}
}

func handleRelay(conn net.Conn, reg *registry.Registry, c Config, ownSocket string) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(firstLineDeadline))
	s := bufio.NewScanner(io.LimitReader(conn, maxLineBytes*maxFrames))
	s.Buffer(make([]byte, 1024), maxLineBytes)
	for n := 0; n < maxFrames && s.Scan(); n++ {
		var f frame
		if json.Unmarshal(s.Bytes(), &f) != nil || f.Type == "auth" {
			continue
		}
		if f.Type != "user" {
			continue
		}
		if err := validateNativeSender(c.Home, c.SocketDir, f.From); err != nil {
			_ = sendReceipt(c.SocketDir, f.From, ownSocket, f.MsgID, "denied", err.Error())
			continue
		}
		if err := queueFrame(reg, c, f, ownSocket); err != nil {
			recordRejection(reg, c, f, s.Bytes(), err)
			rejectFrame(reg, c, f, ownSocket, err)
			continue
		}
		_ = sendReceipt(c.SocketDir, f.From, ownSocket, f.MsgID, "received", "transport accepted; logical source queue updated")
	}
}

func validateNativeSender(home, socketDir, from string) error {
	if !strings.HasPrefix(from, "uds:") {
		return errors.New("sender is not a uds address")
	}
	socket := strings.TrimPrefix(from, "uds:")
	if err := validateTargetSocket(socketDir, socket); err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(socket), ".sock"))
	if err != nil {
		return errors.New("sender socket has no valid owner pid")
	}
	_, record, found, err := nativeRecord(home, socket)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("sender has no current native Claude record")
	}
	if record.Agenthail != "" {
		return errors.New("sender is not a current native Claude endpoint")
	}
	if record.PID != pid || record.ProcStart == "" || !nativeProcessMatches(pid, record.ProcStart) {
		return errors.New("sender native process identity is stale")
	}
	return nil
}

func acceptPeers(wg *sync.WaitGroup, ln net.Listener, reg *registry.Registry, c Config, ownSocket string) {
	sem := make(chan struct{}, 8)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-sem }(); handlePeer(conn, reg, c, ownSocket) }()
	}
}

func handlePeer(conn net.Conn, reg *registry.Registry, c Config, ownSocket string) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(firstLineDeadline))
	s := bufio.NewScanner(io.LimitReader(conn, maxLineBytes*maxFrames))
	s.Buffer(make([]byte, 1024), maxLineBytes)
	type rejection struct {
		frame frame
		err   error
	}
	var rejected []rejection
	var accepted []frame
	defer func() {
		conn.Close()
		for _, item := range accepted {
			_ = sendReceipt(c.SocketDir, item.From, ownSocket, item.MsgID, "received", "transport accepted; logical source queue updated")
		}
		for _, item := range rejected {
			rejectFrame(reg, c, item.frame, ownSocket, item.err)
		}
	}()
	for n := 0; n < maxFrames && s.Scan(); n++ {
		var f frame
		if json.Unmarshal(s.Bytes(), &f) != nil {
			continue
		}
		if f.Type == "auth" {
			continue
		}
		if f.Type == "control" {
			var receipt struct {
				Action   string `json:"action"`
				Status   string `json:"status"`
				Original string `json:"orig_msg_id"`
			}
			if json.Unmarshal(s.Bytes(), &receipt) == nil && receipt.Action == "peer_message_status" {
				_ = reg.RecordHistory(registry.HistoryEntry{Kind: "peer_receipt", SessionID: c.Session.ID, SourceSessionID: f.From, Result: receipt.Status + ":" + receipt.Original})
				recordReceipt(reg, registry.PeerMessage{MsgID: receipt.Original, SenderSessionID: c.Session.ID}, receipt.Status)
			}
			continue
		}
		if err := queueFrame(reg, c, f, ownSocket); err != nil {
			rejected = append(rejected, rejection{frame: f, err: err})
			recordRejection(reg, c, f, s.Bytes(), err)
		} else {
			accepted = append(accepted, f)
		}
	}
}

func queueFrame(reg *registry.Registry, c Config, f frame, ownSocket string) error {
	if f.Type != "user" || f.MsgID == "" || f.From == "" || len(f.Message) == 0 {
		return errors.New("invalid user frame")
	}
	if _, err := uuid.Parse(f.MsgID); err != nil {
		return errors.New("msg_id must be UUID")
	}
	if !strings.HasPrefix(f.From, "uds:") {
		return errors.New("from must be a uds address")
	}
	var body messageBody
	if err := json.Unmarshal(f.Message, &body); err != nil || body.Role != "user" || body.Content == "" {
		return errors.New("invalid user message")
	}
	target, err := reg.Session(c.Session.ID)
	if err != nil {
		return fmt.Errorf("canonical target is not registered: %w", err)
	}
	if err := surface.NoReplyRoute(target); err != nil {
		return err
	}
	key := "peer:" + target.ID + ":" + f.From + ":" + f.MsgID
	// The host that owns a session decides whether it can take a message.
	if owner, err := reg.SessionHost(target.ID); err != nil {
		return err
	} else if owner != "" {
		return queueHostFrame(reg, c, f, target.ID, key, body.Content)
	}
	if target.Status == surface.StatusOffline || surface.IsReadOnlySession(target) {
		reason := surface.ReadOnlySessionReason(target)
		if reason == "" {
			reason = "target session is offline"
		}
		return surface.DeliveryTerminal(errors.New(reason), surface.DeliveryAccessDenied)
	}
	if c.BusyDelivery != "" {
		if _, modeErr := deliverypolicy.Normalize(c.BusyDelivery); modeErr != nil {
			return modeErr
		}
	}
	queueID, err := reg.QueueMessageWithOptions(target.ID, "[Claude peer message from "+f.From+"]\n"+body.Content, key, surface.SendOptions{BusyDelivery: c.BusyDelivery})
	if err != nil {
		return err
	}
	return reg.RecordHistory(registry.HistoryEntry{Kind: "peer_received", SessionID: target.ID, SourceSessionID: sourceEvidence(f), QueueID: queueID, Message: body.Content, Result: "transport accepted; model completion pending"})
}

func sourceEvidence(f frame) string { return f.From }

// recordRejection keeps a refused frame in delivery history as a failure,
// with its message text when the frame carried one.
func recordRejection(reg *registry.Registry, c Config, f frame, raw []byte, err error) {
	message := string(raw)
	var body messageBody
	if json.Unmarshal(f.Message, &body) == nil && body.Content != "" {
		message = body.Content
	}
	_ = reg.RecordHistory(registry.HistoryEntry{Kind: "peer_rejected", SessionID: c.Session.ID, SourceSessionID: sourceEvidence(f), Message: message, Error: err.Error()})
}

// noRouteNoticeInterval bounds how often one sender is told why its messages
// to a session with no reply route are refused, so a model that answers the
// notice cannot start an endless exchange with it. At most
// noRouteNoticeSenders senders are remembered at once; while that many are,
// a new sender gets only its refused receipt.
const (
	noRouteNoticeInterval = 10 * time.Minute
	noRouteNoticeSenders  = 256
)

var noRouteNotices = struct {
	sync.Mutex
	sent map[string]time.Time
}{sent: map[string]time.Time{}}

// takeNoRouteNotice reports whether the sender is due a notice now, and
// records it when it is.
func takeNoRouteNotice(from string, now time.Time) bool {
	noRouteNotices.Lock()
	defer noRouteNotices.Unlock()
	if last, ok := noRouteNotices.sent[from]; ok && now.Sub(last) < noRouteNoticeInterval {
		return false
	}
	for sender, last := range noRouteNotices.sent {
		if now.Sub(last) >= noRouteNoticeInterval {
			delete(noRouteNotices.sent, sender)
		}
	}
	if len(noRouteNotices.sent) >= noRouteNoticeSenders {
		return false
	}
	noRouteNotices.sent[from] = now
	return true
}

// rejectFrame tells a sender its frame was not delivered. Claude shows its
// model a fixed notice for each receipt status and never the receipt's
// detail, so a frame refused for having no reply route is followed by a
// message that gives the reason and where to send instead.
func rejectFrame(reg *registry.Registry, c Config, f frame, ownSocket string, err error) {
	var noRoute surface.NoReplyRouteError
	if !errors.As(err, &noRoute) {
		_ = sendReceipt(c.SocketDir, f.From, ownSocket, f.MsgID, "denied", err.Error())
		return
	}
	_ = sendReceipt(c.SocketDir, f.From, ownSocket, f.MsgID, "refused", err.Error())
	target := strings.TrimPrefix(f.From, "uds:")
	if validateTargetSocket(c.SocketDir, target) != nil {
		return
	}
	if !takeNoRouteNotice(f.From, time.Now()) {
		return
	}
	notice := fmt.Sprintf("Agenthail could not deliver your message %s. %s Messages to this address are refused the same way.", f.MsgID, err)
	payload, _ := json.Marshal(frame{MsgV: peerProtocol, MsgID: uuid.New().String(), Type: "user", Message: mustJSON(messageBody{Role: "user", Content: peerContent(c.Session, replyTarget(reg, c.Session.ID), ownSocket, "", notice)}), Priority: "next", From: "uds:" + ownSocket})
	if framed, err := authenticatedFrame(c.Home, target, payload); err == nil {
		_ = postFrame(target, framed)
	}
}

// queueHostFrame holds a message for a session another host owns, to be sent
// to that host as this sender's own message once it is reachable. The sender
// is named as one of this host's sessions so the owner can reply across the
// pairing; a sender that is not a current native Claude endpoint goes as
// this host's operator.
func queueHostFrame(reg *registry.Registry, c Config, f frame, targetID, key, content string) error {
	send := registry.HostSend{SessionID: targetID, Message: content, Key: key}
	if sender, ok := nativeSenderSession(reg, c, f.From); ok {
		send.SourceSessionID = sender
	} else {
		send.Message = "[Claude peer message from " + f.From + "]\n" + content
	}
	queueID, err := reg.QueueHostSend(send)
	if err != nil {
		return err
	}
	return reg.RecordHistory(registry.HistoryEntry{Kind: "peer_received", SessionID: targetID, SourceSessionID: sourceEvidence(f), QueueID: queueID, Message: content, Result: "held for its host; sent once the host is reachable"})
}

// nativeSenderSession resolves a frame's sender to this host's registered
// Claude session behind its messaging socket.
func nativeSenderSession(reg *registry.Registry, c Config, from string) (string, bool) {
	if validateNativeSender(c.Home, c.SocketDir, from) != nil {
		return "", false
	}
	_, record, found, err := nativeRecord(c.Home, strings.TrimPrefix(from, "uds:"))
	if err != nil || !found || record.SessionID == "" {
		return "", false
	}
	session, err := reg.Session(record.SessionID)
	if err != nil {
		return "", false
	}
	if owner, err := reg.SessionHost(session.ID); err != nil || owner != "" {
		return "", false
	}
	return session.ID, true
}

func acceptControls(wg *sync.WaitGroup, ln net.Listener, reg *registry.Registry, c Config, ownSocket string) {
	sem := make(chan struct{}, 8)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-sem }(); handleControl(conn, reg, c, ownSocket) }()
	}
}

// recordReceipt keeps the delivery equal to what the target's Claude reported
// to its sender. A hold is left to the target's host, which knows its cause;
// the receipt's final outcome stands on its own.
func recordReceipt(reg *registry.Registry, message registry.PeerMessage, status string) {
	switch status {
	case "delivered":
		_, _ = reg.ReleasePeerMessage(message)
	case "expired", "refused", "denied", "dropped":
		_, _ = reg.DropPeerMessage(message, registry.PeerDroppedReason(status))
	}
}

func handleControl(conn net.Conn, reg *registry.Registry, c Config, ownSocket string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(readDeadline))
	var req controlRequest
	err := json.NewDecoder(io.LimitReader(conn, maxLineBytes)).Decode(&req)
	resp := controlResponse{}
	if err != nil {
		resp.Error = &controlError{Class: "terminal", Kind: string(surface.DeliveryInvalidRequest), Error: err.Error()}
	} else if req.SenderID != c.Session.ID || strings.TrimSpace(req.Message) == "" {
		resp.Error = &controlError{Class: "terminal", Kind: string(surface.DeliveryInvalidRequest), Error: "sender identity mismatch or empty message"}
	} else if err := validateTargetSocket(c.SocketDir, req.TargetSocket); err != nil {
		resp.Error = classifyControlError(err)
	} else {
		session, err := reg.Session(c.Session.ID)
		if err != nil {
			resp.Error = &controlError{Class: "terminal", Kind: string(surface.DeliveryTargetMissing), Error: "sender session is no longer registered"}
			_ = json.NewEncoder(conn).Encode(resp)
			return
		}
		id := messageID(req.MsgID)
		senderSocket := ownSocket
		if req.ReplySocket != "" {
			if err := validateRelaySocket(c.SocketDir, req.ReplySocket); err != nil {
				resp.Error = classifyControlError(err)
				_ = json.NewEncoder(conn).Encode(resp)
				return
			}
			senderSocket = req.ReplySocket
		}
		sender := *session
		if sender.ID == surface.PersonSessionID {
			if sender.Name = req.PersonName; sender.Name == "" {
				if sender.Name, err = reg.PersonName(); err != nil {
					resp.Error = classifyControlError(err)
					_ = json.NewEncoder(conn).Encode(resp)
					return
				}
			}
		}
		content := peerContent(sender, replyTarget(reg, sender.ID), senderSocket, "", req.Message)
		f, _ := json.Marshal(frame{MsgV: 1, MsgID: id, UUID: id, Type: "user", Message: mustJSON(messageBody{Role: "user", Content: content}), Priority: "next", From: "uds:" + senderSocket})
		var sendErr error
		if len(f) >= maxLineBytes {
			sendErr = surface.DeliveryTerminal(errors.New("Claude peer message exceeds the 64 KiB frame limit"), surface.DeliveryInvalidRequest)
		} else {
			sendErr = sendAuthenticatedFrame(c.Home, req.TargetSocket, f)
		}
		if err := sendErr; err != nil {
			resp.Error = classifyControlError(err)
		} else {
			resp.OK = true
			resp.Result = &surface.SendResult{UUID: id, Accepted: true}
		}
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

func validateRelaySocket(socketDir, path string) error {
	return validateTargetSocket(socketDir, path)
}

func classifyControlError(err error) *controlError {
	var terminal surface.DeliveryTerminalError
	if errors.As(err, &terminal) {
		return &controlError{Class: "terminal", Kind: string(terminal.Kind), Error: terminal.Error()}
	}
	var unavailable surface.DeliveryUnavailableError
	if errors.As(err, &unavailable) {
		return &controlError{Class: "unavailable", Error: unavailable.Error()}
	}
	return &controlError{Class: "unknown", Error: err.Error()}
}

// ClaudeSocketDirs returns the directories Claude Code binds peer sockets in:
// /tmp/cc-socks, and on Linux $XDG_RUNTIME_DIR/cc-socks or the default
// /run/user/<uid>/cc-socks, matching Claude Code's own default namespaces.
func ClaudeSocketDirs() []string {
	return claudeSocketDirsFor(runtime.GOOS, os.Getenv("XDG_RUNTIME_DIR"), os.Getuid())
}

func claudeSocketDirsFor(goos, runtimeDir string, uid int) []string {
	dirs := []string{DefaultSocketDir}
	if goos != "linux" {
		return dirs
	}
	if runtimeDir = strings.TrimSpace(runtimeDir); runtimeDir != "" {
		dirs = append(dirs, filepath.Join(runtimeDir, "cc-socks"))
	}
	if dir := filepath.Join("/run/user", strconv.Itoa(uid), "cc-socks"); !slices.Contains(dirs, dir) {
		dirs = append(dirs, dir)
	}
	return dirs
}

func validateTargetSocket(dir, target string) error {
	if dir == "" {
		dir = defaultSocketDir
	}
	cleanDir, _ := filepath.Abs(dir)
	cleanTarget, _ := filepath.Abs(target)
	targetDir := filepath.Dir(cleanTarget)
	if (targetDir != cleanDir && !slices.Contains(ClaudeSocketDirs(), targetDir)) || !strings.HasSuffix(filepath.Base(cleanTarget), ".sock") {
		return surface.DeliveryTerminal(errors.New("target socket is outside the Claude socket namespace"), surface.DeliveryInvalidRequest)
	}
	st, err := os.Lstat(cleanTarget)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	if st.Mode()&os.ModeSymlink != 0 || st.Mode()&os.ModeSocket == 0 {
		return surface.DeliveryTerminal(errors.New("target is not a socket"), surface.DeliveryInvalidRequest)
	}
	pidText := strings.TrimSuffix(filepath.Base(cleanTarget), ".sock")
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return surface.DeliveryTerminal(errors.New("target socket has no valid owner pid"), surface.DeliveryInvalidRequest)
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return surface.DeliveryUnavailable(fmt.Errorf("target owner is not running: %w", err))
	}
	if stat, ok := st.Sys().(*syscall.Stat_t); ok && uint32(stat.Uid) != uint32(os.Getuid()) {
		return surface.DeliveryTerminal(errors.New("target socket is not owned by this user"), surface.DeliveryAccessDenied)
	}
	return nil
}

func sendReceipt(socketDir, from, ownSocket, msgID, status, detail string) error {
	if !strings.HasPrefix(from, "uds:") {
		return errors.New("receipt address is not uds")
	}
	target := strings.TrimPrefix(from, "uds:")
	if err := validateTargetSocket(socketDir, target); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"type": "control", "action": "peer_message_status", "status": status, "orig_msg_id": msgID, "from": "uds:" + ownSocket, "detail": detail})
	return postFrame(target, payload)
}

// postFrame writes a frame nothing waits on, such as a receipt, and closes
// without waiting for the sender to close first: a sender that accepts and
// never closes would otherwise hold the connection that is answering it for
// the full read deadline on every frame.
func postFrame(target string, payload []byte) error {
	conn, err := net.DialTimeout("unix", target, noticeDeadline)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(noticeDeadline))
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return surface.DeliveryOutcomeUnknown(err)
	}
	return nil
}

func sendFrame(target string, payload []byte) error {
	conn, err := net.DialTimeout("unix", target, readDeadline)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(readDeadline))
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return surface.DeliveryOutcomeUnknown(err)
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	b := make([]byte, 1)
	_, err = conn.Read(b)
	if err != nil && !errors.Is(err, io.EOF) {
		return surface.DeliveryOutcomeUnknown(err)
	}
	if err == nil {
		return surface.DeliveryOutcomeUnknown(errors.New("unexpected response bytes from Claude socket"))
	}
	return nil
}

// nativeRecord reads the session record of the Claude process serving
// socket, and the config root that holds it.
func nativeRecord(home, socket string) (string, sessionRecord, bool, error) {
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		return "", sessionRecord{}, false, err
	}
	for _, path := range surface.ClaudeSessionRecordPaths(roots, strings.TrimSuffix(filepath.Base(socket), ".sock")) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record sessionRecord
		if json.Unmarshal(data, &record) == nil && record.MessagingSocketPath == socket {
			return filepath.Dir(filepath.Dir(path)), record, true, nil
		}
	}
	return "", sessionRecord{}, false, nil
}

func sendAuthenticatedFrame(home, target string, payload []byte) error {
	framed, err := authenticatedFrame(home, target, payload)
	if err != nil {
		return err
	}
	return sendFrame(target, framed)
}

// authenticatedFrame prefixes payload with the peer key Claude wrote beside
// the target process's session record, in whichever config root holds that
// record.
func authenticatedFrame(home, target string, payload []byte) ([]byte, error) {
	hash := sha256.Sum256([]byte(target))
	pid := strings.TrimSuffix(filepath.Base(target), ".sock")
	root, _, found, err := nativeRecord(home, target)
	if err != nil {
		return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	if !found {
		root = surface.ClaudeConfigDir(home)
	}
	keyPath := filepath.Join(root, "sessions", pid+"."+hex.EncodeToString(hash[:])+".key")
	if info, err := os.Lstat(keyPath); err == nil {
		if !info.Mode().IsRegular() {
			return nil, surface.DeliveryTerminal(errors.New("invalid Claude peer key file"), surface.DeliveryAuthenticationNeeded)
		}
		var key struct {
			PeerToken string `json:"peerToken"`
		}
		f, err := os.Open(keyPath)
		if err != nil {
			return nil, surface.DeliveryUnavailable(err)
		}
		err = json.NewDecoder(io.LimitReader(f, 8192)).Decode(&key)
		f.Close()
		if err != nil || key.PeerToken == "" {
			return nil, surface.DeliveryTerminal(errors.New("invalid Claude peer key"), surface.DeliveryAuthenticationNeeded)
		}
		auth, _ := json.Marshal(map[string]string{"type": "auth", "token": key.PeerToken})
		payload = append(append(auth, '\n'), payload...)
	} else if !os.IsNotExist(err) {
		return nil, surface.DeliveryUnavailable(err)
	}
	return payload, nil
}

func dialContext(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

var closingPeerTag = regexp.MustCompile(`(?i)<(\s*/\s*cross-session-message\b)`)

// messageID is the delivery's own uuid when it has one, so a report on the
// message names the delivery it belongs to.
func messageID(id string) string {
	if _, err := uuid.Parse(id); err == nil {
		return id
	}
	return uuid.New().String()
}

// peerContent wraps a message from session. The sender is named by target,
// how a reply reaches it, which is never shortened; without one, by its
// title, shortened to 64 runes.
func peerContent(session surface.Session, target, socket, fromMode, message string) string {
	name := target
	if name == "" {
		name = strings.TrimSpace(session.Name)
		if runes := []rune(name); len(runes) > 64 {
			name = string(runes[:64]) + "…"
		}
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '"' || r == '<' || r == '>' || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Cs, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, PeerName(session.Surface, name)))
	switch session.ID {
	case surface.PersonSessionID:
		message = session.Name + " wrote this in Agenthail. They read your reply here in this session; answer in your reply, not with SendMessage.\n\n" + message
	case surface.CLISessionID:
		message = "Sent with agenthail send from outside any session; no one reads replies to it.\n\n" + message
	}
	message = closingPeerTag.ReplaceAllString(message, `<\$1`)
	mode := ""
	if fromMode == "bypass" || fromMode == "prompting" {
		mode = ` from-mode="` + fromMode + `"`
	}
	return fmt.Sprintf("<cross-session-message from=\"uds:%s\" from-session=\"%s\" from-name=\"%s\"%s>\n%s\n</cross-session-message>", socket, surface.PeerUUID(session.Surface, session.ID), name, mode, message)
}
