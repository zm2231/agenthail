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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/google/uuid"
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
)

type Config struct {
	Home         string
	RegistryPath string
	Session      surface.Session
	SocketDir    string
}

type Ready struct {
	PID         int    `json:"pid"`
	SocketPath  string `json:"socketPath"`
	ControlPath string `json:"controlPath"`
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
}

type frame struct {
	MsgV     int             `json:"msgV"`
	MsgID    string          `json:"msg_id"`
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

func ControlPath(home, senderID string) string {
	h := sha256.Sum256([]byte(senderID))
	return filepath.Join(home, ".agenthail", "peers", hex.EncodeToString(h[:])[:24]+".sock")
}

func RunWorker(ctx context.Context, config Config, parent io.Reader, ready io.Writer) error {
	c, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	pid := os.Getpid()
	procStart, err := processStart(pid)
	if err != nil {
		return fmt.Errorf("read process start: %w", err)
	}
	claudeID := canonicalID(c.Session.Surface, c.Session.ID)
	socketPath := filepath.Join(c.SocketDir, strconv.Itoa(pid)+".sock")
	controlPath := ControlPath(c.Home, c.Session.ID)
	if err := os.MkdirAll(c.SocketDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(controlPath), 0700); err != nil {
		return err
	}
	ln, err := listenOwned(socketPath)
	if err != nil {
		return fmt.Errorf("listen peer socket: %w", err)
	}
	cl, err := listenOwned(controlPath)
	if err != nil {
		ln.Close()
		os.Remove(socketPath)
		return fmt.Errorf("listen control socket: %w", err)
	}
	started := time.Now().UnixMilli()
	recordPath := filepath.Join(c.Home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	record := sessionRecord{PID: pid, SessionID: claudeID, Cwd: c.Session.Cwd, StartedAt: started, ProcStart: procStart,
		Version: "agenthail", PeerProtocol: peerProtocol, Kind: "interactive", Entrypoint: "cli", MessagingSocketPath: socketPath,
		Name: "agenthail/" + string(c.Session.Surface) + ": " + c.Session.Name, NameSince: started, Status: status(c.Session), UpdatedAt: started, StatusUpdatedAt: started, Agenthail: "peer-worker"}
	if err := writeExclusiveJSON(recordPath, record); err != nil {
		ln.Close()
		cl.Close()
		os.Remove(socketPath)
		os.Remove(controlPath)
		return err
	}
	reg, err := registry.Open(c.RegistryPath)
	if err != nil {
		cleanupOwned(recordPath, record)
		ln.Close()
		cl.Close()
		os.Remove(socketPath)
		os.Remove(controlPath)
		return err
	}
	defer reg.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var listeners, handlers sync.WaitGroup
	listeners.Add(3)
	go func() { defer listeners.Done(); heartbeat(ctx.Done(), recordPath, record, reg, c.Session.ID) }()
	go func() { defer listeners.Done(); acceptPeers(&handlers, ln, reg, c, socketPath) }()
	go func() { defer listeners.Done(); acceptControls(&handlers, cl, reg, c, socketPath) }()
	defer func() {
		cancel()
		ln.Close()
		cl.Close()
		listeners.Wait()
		handlers.Wait()
		cleanupOwned(recordPath, record)
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

func Send(ctx context.Context, home, senderID, targetSocket, message string) (*surface.SendResult, error) {
	if strings.TrimSpace(senderID) == "" || strings.TrimSpace(message) == "" {
		return nil, surface.DeliveryTerminal(errors.New("sender and message are required"), surface.DeliveryInvalidRequest)
	}
	control := ControlPath(home, senderID)
	conn, err := dialContext(ctx, control)
	if err != nil {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("peer control socket: %w", err))
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(readDeadline)); err != nil {
		return nil, err
	}
	req, _ := json.Marshal(controlRequest{TargetSocket: targetSocket, SenderID: senderID, Message: message})
	if len(req) >= maxLineBytes {
		return nil, surface.DeliveryTerminal(errors.New("Claude peer message exceeds the 64 KiB frame limit"), surface.DeliveryInvalidRequest)
	}
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
	return c, nil
}

func canonicalID(kind surface.SurfaceKind, id string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(string(kind)+":"+id)).String()
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

func processStart(pid int) (string, error) {
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
	return ln, nil
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
	closeErr := f.Close()
	if encodeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(encodeErr, closeErr)
	}
	return nil
}

func heartbeat(stop <-chan struct{}, path string, record sessionRecord, reg *registry.Registry, id string) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			if session, err := reg.Session(id); err == nil {
				record.Name = "agenthail/" + string(session.Surface) + ": " + session.Name
				if alias, err := reg.ReverseAlias(id); err == nil && alias != "" {
					record.Name = "agenthail/" + string(session.Surface) + ": " + alias
				}
				record.Cwd = session.Cwd
				record.Status = status(*session)
			}
			record.UpdatedAt = now.UnixMilli()
			record.StatusUpdatedAt = record.UpdatedAt
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				_ = writeExclusiveJSON(path, record)
				continue
			}
			tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
			f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				continue
			}
			encodeErr := json.NewEncoder(f).Encode(record)
			closeErr := f.Close()
			if encodeErr == nil && closeErr == nil && recordOwned(path, record) {
				_ = os.Rename(tmp, path)
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
	return json.Unmarshal(data, &current) == nil && current.Agenthail == record.Agenthail && current.PID == record.PID && current.SessionID == record.SessionID && current.ProcStart == record.ProcStart
}

func cleanupOwned(path string, record sessionRecord) {
	if recordOwned(path, record) {
		_ = os.Remove(path)
	}
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
		frame  frame
		detail string
	}
	var rejected []rejection
	defer func() {
		conn.Close()
		for _, item := range rejected {
			_ = sendReceipt(c.SocketDir, item.frame.From, ownSocket, item.frame.MsgID, "denied", item.detail)
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
			}
			continue
		}
		if err := queueFrame(reg, c, f, ownSocket); err != nil {
			rejected = append(rejected, rejection{frame: f, detail: err.Error()})
			_ = reg.RecordHistory(registry.HistoryEntry{Kind: "peer_rejected", SessionID: c.Session.ID, Message: string(s.Bytes()), Error: err.Error()})
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
	if target.Surface == surface.SurfaceKind("agenthail") {
		return reg.RecordHistory(registry.HistoryEntry{Kind: "peer_received", SessionID: target.ID, SourceSessionID: sourceEvidence(f), Message: body.Content, Result: "operator sink; no model queue"})
	}
	if target.Status == surface.StatusOffline || surface.IsReadOnlySession(target) {
		reason := surface.ReadOnlySessionReason(target)
		if reason == "" {
			reason = "target session is offline"
		}
		return surface.DeliveryTerminal(errors.New(reason), surface.DeliveryAccessDenied)
	}
	key := "peer:" + target.ID + ":" + f.From + ":" + f.MsgID
	queueID, err := reg.QueueMessageWithOptions(target.ID, "[Claude peer message from "+f.From+"]\n"+body.Content, key, surface.SendOptions{})
	if err != nil {
		return err
	}
	return reg.RecordHistory(registry.HistoryEntry{Kind: "peer_received", SessionID: target.ID, SourceSessionID: sourceEvidence(f), QueueID: queueID, Message: body.Content, Result: "transport accepted; model completion pending"})
}

func sourceEvidence(f frame) string { return f.From }

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
		if alias, err := reg.ReverseAlias(session.ID); err == nil && alias != "" {
			session.Name = alias
		}
		id := uuid.New().String()
		content := peerContent(*session, ownSocket, req.Message)
		f, _ := json.Marshal(frame{MsgV: 1, MsgID: id, Type: "user", Message: mustJSON(messageBody{Role: "user", Content: content}), Priority: "next", From: "uds:" + ownSocket})
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
			_ = reg.RecordHistory(registry.HistoryEntry{Kind: "peer_sent", SessionID: c.Session.ID, SourceSessionID: req.SenderID, Message: req.Message, Result: id + ": transport accepted; model completion pending"})
		}
	}
	_ = json.NewEncoder(conn).Encode(resp)
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

func validateTargetSocket(dir, target string) error {
	if dir == "" {
		dir = defaultSocketDir
	}
	cleanDir, _ := filepath.Abs(dir)
	cleanTarget, _ := filepath.Abs(target)
	if filepath.Dir(cleanTarget) != cleanDir || !strings.HasSuffix(filepath.Base(cleanTarget), ".sock") {
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
	return sendFrame(target, payload)
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

func sendAuthenticatedFrame(home, target string, payload []byte) error {
	hash := sha256.Sum256([]byte(target))
	pid := strings.TrimSuffix(filepath.Base(target), ".sock")
	keyPath := filepath.Join(home, ".claude", "sessions", pid+"."+hex.EncodeToString(hash[:])+".key")
	if info, err := os.Lstat(keyPath); err == nil {
		if !info.Mode().IsRegular() {
			return surface.DeliveryTerminal(errors.New("invalid Claude peer key file"), surface.DeliveryAuthenticationNeeded)
		}
		var key struct {
			PeerToken string `json:"peerToken"`
		}
		f, err := os.Open(keyPath)
		if err != nil {
			return surface.DeliveryUnavailable(err)
		}
		err = json.NewDecoder(io.LimitReader(f, 8192)).Decode(&key)
		f.Close()
		if err != nil || key.PeerToken == "" {
			return surface.DeliveryTerminal(errors.New("invalid Claude peer key"), surface.DeliveryAuthenticationNeeded)
		}
		auth, _ := json.Marshal(map[string]string{"type": "auth", "token": key.PeerToken})
		payload = append(append(auth, '\n'), payload...)
	} else if !os.IsNotExist(err) {
		return surface.DeliveryUnavailable(err)
	}
	return sendFrame(target, payload)
}

func dialContext(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

var closingPeerTag = regexp.MustCompile(`(?i)<(\s*/\s*cross-session-message\b)`)

func peerContent(session surface.Session, socket, message string) string {
	name := strings.Map(func(r rune) rune {
		if r == '"' || r == '<' || r == '>' || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Cs, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, "agenthail/"+string(session.Surface)+": "+session.Name)
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > 64 {
		name = string(runes[:64]) + "…"
	}
	message = closingPeerTag.ReplaceAllString(message, `<\$1`)
	return fmt.Sprintf("<cross-session-message from=\"uds:%s\" from-session=\"%s\" from-name=\"%s\">\n%s\n</cross-session-message>", socket, canonicalID(session.Surface, session.ID), name, message)
}
