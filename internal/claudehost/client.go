package claudehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"time"
)

// Protocol is the host protocol this build speaks. A host that answers hello
// with another protocol or version is replaced once it is idle.
const Protocol = 1

const maxResponseBytes = 4 << 20

// ErrNotRunning means no host answered on the socket. Nothing was sent.
var ErrNotRunning = errors.New("the Claude host is not running")

// Error is the host's answer to a failed request. Its Code says what
// happened: only "unknown" means the message may still reach Claude.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func IsCode(err error, code string) bool {
	var hostErr *Error
	return errors.As(err, &hostErr) && hostErr.Code == code
}

// OutcomeUnknownError means the request was written but no answer arrived,
// so the host may or may not have acted on it.
type OutcomeUnknownError struct{ Err error }

func (e *OutcomeUnknownError) Error() string {
	return fmt.Sprintf("the Claude host did not answer: %s", e.Err)
}

func (e *OutcomeUnknownError) Unwrap() error { return e.Err }

type Hello struct {
	Protocol    int    `json:"protocol"`
	HostVersion string `json:"hostVersion"`
	PID         int    `json:"pid"`
}

type OpenRequest struct {
	SessionID      string `json:"sessionId,omitempty"`
	Resume         string `json:"resume,omitempty"`
	Cwd            string `json:"cwd"`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Agent          string `json:"agent,omitempty"`
	Name           string `json:"name,omitempty"`
	Worktree       string `json:"worktree,omitempty"`
	ConfigDir      string `json:"configDir,omitempty"`
}

type Session struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Cwd       string `json:"cwd"`
	State     string `json:"state"`
	OpenedAt  string `json:"openedAt"`
}

type Origin struct {
	Kind   string `json:"kind"`
	From   string `json:"from,omitempty"`
	Name   string `json:"name,omitempty"`
	Server string `json:"server,omitempty"`
}

type SendRequest struct {
	SessionID string  `json:"sessionId"`
	UUID      string  `json:"uuid"`
	Text      string  `json:"text"`
	Images    []Image `json:"images,omitempty"`
	Priority  string  `json:"priority,omitempty"`
	Origin    *Origin `json:"origin,omitempty"`
}

// Image is a stored image file the host reads and gives Claude as an image
// block before the text. The host runs on the Mac that stores the upload.
type Image struct {
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
}

type SendResult struct {
	Accepted  bool `json:"accepted"`
	Duplicate bool `json:"duplicate"`
}

// Orphan is a Claude process an earlier host started that still runs. It
// holds its session until it exits.
type Orphan struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Cwd       string `json:"cwd"`
}

type Listing struct {
	Sessions []Session `json:"sessions"`
	Orphans  []Orphan  `json:"orphans"`
	Draining bool      `json:"draining"`
}

type Event struct {
	Seq       uint64          `json:"seq"`
	SessionID string          `json:"sessionId"`
	Kind      string          `json:"kind"`
	Message   json.RawMessage `json:"message,omitempty"`
	PID       int             `json:"pid,omitempty"`
	State     string          `json:"state,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type Client struct {
	Socket string
}

func SocketPath(home string) string {
	return filepath.Join(home, ".agenthail", "claude-host.sock")
}

func NewClient(home string) Client { return Client{Socket: SocketPath(home)} }

func (c Client) Hello(ctx context.Context) (Hello, error) {
	var out Hello
	return out, c.call(ctx, "hello", struct{}{}, &out)
}

func (c Client) Open(ctx context.Context, request OpenRequest) (Session, error) {
	var out Session
	return out, c.call(ctx, "open", request, &out)
}

func (c Client) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	var out SendResult
	return out, c.call(ctx, "send", request, &out)
}

func (c Client) Interrupt(ctx context.Context, sessionID string) error {
	return c.call(ctx, "interrupt", map[string]string{"sessionId": sessionID}, nil)
}

func (c Client) Close(ctx context.Context, sessionID string) error {
	return c.call(ctx, "close", map[string]string{"sessionId": sessionID}, nil)
}

func (c Client) List(ctx context.Context) (Listing, error) {
	var out Listing
	return out, c.call(ctx, "list", struct{}{}, &out)
}

func (c Client) Drain(ctx context.Context) error {
	return c.call(ctx, "drain", struct{}{}, nil)
}

type request struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
	Event  *Event          `json:"event"`
}

func (c Client) dial(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotRunning, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	return conn, nil
}

func (c Client) call(ctx context.Context, method string, params, out any) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	payload, err := json.Marshal(request{ID: 1, Method: method, Params: params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return &OutcomeUnknownError{Err: err}
	}
	reader := bufio.NewReaderSize(conn, 64<<10)
	line, err := readLine(reader)
	if err != nil {
		return &OutcomeUnknownError{Err: err}
	}
	var reply response
	if err := json.Unmarshal(line, &reply); err != nil {
		return &OutcomeUnknownError{Err: fmt.Errorf("decode %s reply: %w", method, err)}
	}
	if reply.Error != nil {
		return reply.Error
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(reply.Result, out); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}

// Subscribe streams events after the given sequence number until ctx ends or
// the connection drops. gap reports that events after `after` were dropped
// from the host's buffer, so the caller re-reads state instead of replaying.
func (c Client) Subscribe(ctx context.Context, after uint64, onEvent func(Event)) (gap bool, err error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Time{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	payload, _ := json.Marshal(request{ID: 1, Method: "subscribe", Params: map[string]uint64{"after": after}})
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return false, err
	}
	reader := bufio.NewReaderSize(conn, 64<<10)
	line, err := readLine(reader)
	if err != nil {
		return false, err
	}
	var reply response
	if err := json.Unmarshal(line, &reply); err != nil {
		return false, err
	}
	var started struct {
		Gap bool `json:"gap"`
	}
	if reply.Error != nil {
		return false, reply.Error
	}
	_ = json.Unmarshal(reply.Result, &started)
	if started.Gap {
		return true, nil
	}
	for {
		line, err := readLine(reader)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, err
		}
		var message response
		if err := json.Unmarshal(line, &message); err != nil {
			return false, err
		}
		if message.Event != nil {
			onEvent(*message.Event)
		}
	}
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > maxResponseBytes {
			return nil, fmt.Errorf("Claude host reply exceeds %d bytes", maxResponseBytes)
		}
		if !isPrefix {
			return line, nil
		}
	}
}
