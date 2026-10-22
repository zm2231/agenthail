package claudehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Protocol is the host protocol this build speaks. A host that answers hello
// with another protocol or version is replaced: it retires and serves its
// sessions until each closes, or is stopped when it cannot retire.
const Protocol = 3

const maxResponseBytes = 4 << 20

// ErrNotRunning means no host answered on the socket. Nothing was sent.
var ErrNotRunning = errors.New("the Claude host is not running")

// ErrOtherProtocol means the host on the socket speaks another protocol than
// this build, as an older host does until the daemon replaces it. Nothing was
// sent.
var ErrOtherProtocol = errors.New("the Claude host speaks another protocol")

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
	// CloseStops is set by hosts whose close interrupts a running turn and
	// returns once Claude has exited.
	CloseStops bool `json:"closeStops"`
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
	SessionID      string       `json:"sessionId"`
	PID            int          `json:"pid"`
	Cwd            string       `json:"cwd"`
	State          string       `json:"state"`
	OpenedAt       string       `json:"openedAt"`
	PermissionMode string       `json:"permissionMode"`
	Model          string       `json:"model"`
	ContextWindow  int64        `json:"contextWindow"`
	ContextModel   string       `json:"contextModel"`
	AutoCompact    *AutoCompact `json:"autoCompact"`
	Requests       []Request    `json:"requests"`
	// Tasks are the live background tasks Claude last reported. A host that
	// predates them leaves it nil.
	Tasks []Task `json:"tasks"`
}

// AutoCompact is when Claude compacts the session's main model, as Claude
// reported it; Window and Threshold are absent while it is off.
type AutoCompact struct {
	Enabled   bool   `json:"enabled"`
	Window    int64  `json:"window"`
	Threshold int64  `json:"threshold"`
	DecidedBy string `json:"decidedBy"`
}

// Task is a background task Claude runs for a session: Type is Claude's task
// type (local_bash, local_agent, monitor_mcp, ...), Ambient marks a task
// Claude says is not activity, and Since is when the host first saw it.
type Task struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	ParentID    string    `json:"parentId"`
	Ambient     bool      `json:"ambient"`
	Since       time.Time `json:"since"`
}

// Request is a tool permission or a question Claude asked through the host,
// which holds it until a person answers it. RequestID is Claude's own id for
// it. Input is the tool's input; a question carries Questions instead.
type Request struct {
	RequestID      string          `json:"requestId"`
	ToolUseID      string          `json:"toolUseId"`
	Tool           string          `json:"tool"`
	Kind           string          `json:"kind"`
	CreatedAt      time.Time       `json:"createdAt"`
	AlwaysAllow    bool            `json:"alwaysAllow"`
	Title          string          `json:"title,omitempty"`
	DisplayName    string          `json:"displayName,omitempty"`
	Description    string          `json:"description,omitempty"`
	DecisionReason string          `json:"decisionReason,omitempty"`
	BlockedPath    string          `json:"blockedPath,omitempty"`
	AgentID        string          `json:"agentId,omitempty"`
	Input          json.RawMessage `json:"input,omitempty"`
	Questions      []Question      `json:"questions,omitempty"`
	Elicitation    *Elicitation    `json:"elicitation,omitempty"`
}

// Elicitation is an MCP server's request for input: a form of fields, or in
// url mode a link to open.
type Elicitation struct {
	Server  string             `json:"server"`
	Message string             `json:"message"`
	Mode    string             `json:"mode"`
	URL     string             `json:"url,omitempty"`
	Fields  []ElicitationField `json:"fields,omitempty"`
}

type ElicitationField struct {
	Name        string              `json:"name"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Type        string              `json:"type"`
	Required    bool                `json:"required"`
	MultiSelect bool                `json:"multiSelect"`
	Options     []ElicitationOption `json:"options,omitempty"`
	Default     json.RawMessage     `json:"default,omitempty"`
}

type ElicitationOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type Question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	MultiSelect bool             `json:"multiSelect"`
	Options     []QuestionOption `json:"options"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Decision answers a Request: allow, optionally always, or deny with a
// message. A question is allowed with an answer keyed by each question.
type Decision struct {
	Behavior string            `json:"behavior"`
	Always   bool              `json:"always,omitempty"`
	Message  string            `json:"message,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
	Content  json.RawMessage   `json:"content,omitempty"`
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

// Listing is what the hosts run. Unanswered names the sockets of hosts that
// did not answer, whose sessions it cannot name.
type Listing struct {
	Sessions   []Session `json:"sessions"`
	Orphans    []Orphan  `json:"orphans"`
	Draining   bool      `json:"draining"`
	Unanswered []string  `json:"-"`
}

type Event struct {
	Seq       uint64          `json:"seq"`
	SessionID string          `json:"sessionId"`
	Kind      string          `json:"kind"`
	Message   json.RawMessage `json:"message,omitempty"`
	PID       int             `json:"pid,omitempty"`
	State     string          `json:"state,omitempty"`
	Request   *Request        `json:"request,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
	Outcome   string          `json:"outcome,omitempty"`
	Error     string          `json:"error,omitempty"`
	Tasks     []Task          `json:"tasks,omitempty"`
}

// Client reaches the host on Socket and, for a session that host does not
// run, the retiring hosts beside it that still serve their own sessions.
type Client struct {
	Socket string
}

func SocketPath(home string) string {
	return filepath.Join(home, ".agenthail", "claude-host.sock")
}

func NewClient(home string) Client { return Client{Socket: SocketPath(home)} }

// Retiring are the hosts that gave up Socket to a newer one and still serve
// the sessions they ran, each on a socket named by its pid. A socket nothing
// listens on is left by a retiring host that died; it is removed.
func (c Client) Retiring() []Client {
	paths, _ := filepath.Glob(strings.TrimSuffix(c.Socket, ".sock") + ".*.sock")
	var out []Client
	for _, path := range paths {
		var dialer net.Dialer
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, err := dialer.DialContext(ctx, "unix", path)
		cancel()
		if errors.Is(err, syscall.ECONNREFUSED) {
			_ = os.Remove(path)
			continue
		}
		if err == nil {
			conn.Close()
		}
		out = append(out, Client{Socket: path})
	}
	return out
}

func (c Client) Hello(ctx context.Context) (Hello, error) {
	var out Hello
	return out, c.call(ctx, "hello", struct{}{}, &out)
}

// Open opens a session in a host of this build's protocol. A conversation a
// retiring host still runs is held by the current one; Open returns it as that
// host lists it.
func (c Client) Open(ctx context.Context, request OpenRequest) (Session, error) {
	var out Session
	hello, err := c.Hello(ctx)
	if err == nil && hello.Protocol != Protocol {
		err = fmt.Errorf("%w: pid %d runs %s protocol %d, and this build opens sessions with protocol %d", ErrOtherProtocol, hello.PID, hello.HostVersion, hello.Protocol, Protocol)
	}
	if err == nil {
		err = c.call(ctx, "open", request, &out)
	}
	if request.Resume == "" || !(IsCode(err, "held") || errors.Is(err, ErrNotRunning)) {
		return out, err
	}
	listing, listErr := c.List(ctx)
	if listErr != nil {
		return out, err
	}
	for _, session := range listing.Sessions {
		if session.SessionID == request.Resume {
			return session, nil
		}
	}
	return out, err
}

func (c Client) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	var out SendResult
	return out, c.sessionCall(ctx, "send", request, &out)
}

func (c Client) Interrupt(ctx context.Context, sessionID string) error {
	return c.sessionCall(ctx, "interrupt", map[string]string{"sessionId": sessionID}, nil)
}

// Respond answers an open request and returns how the host resolved it. A
// request that was answered or withdrawn fails with code "stale".
func (c Client) Respond(ctx context.Context, sessionID, requestID string, decision Decision) (string, error) {
	var out struct {
		Outcome string `json:"outcome"`
	}
	err := c.sessionCall(ctx, "respond", map[string]any{"sessionId": sessionID, "requestId": requestID, "decision": decision}, &out)
	return out.Outcome, err
}

func (c Client) SetModel(ctx context.Context, sessionID, model string) (Session, error) {
	var out Session
	return out, c.sessionCall(ctx, "setModel", map[string]string{"sessionId": sessionID, "model": model}, &out)
}

func (c Client) SetPermissionMode(ctx context.Context, sessionID, mode string) (Session, error) {
	var out Session
	return out, c.sessionCall(ctx, "setPermissionMode", map[string]string{"sessionId": sessionID, "permissionMode": mode}, &out)
}

// Logs is the tail of what an open session's Claude process wrote to stderr.
type Logs struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Stderr    string `json:"stderr"`
}

func (c Client) Logs(ctx context.Context, sessionID string) (Logs, error) {
	var out Logs
	return out, c.sessionCall(ctx, "logs", map[string]string{"sessionId": sessionID}, &out)
}

// Close stops a session now, on whichever host runs it, and returns once its
// Claude has exited.
func (c Client) Close(ctx context.Context, sessionID string) error {
	return c.close(ctx, sessionID, false)
}

// CloseIdle closes a session only if it is idle when the host gets the close,
// and otherwise refuses with busy, leaving a turn that started since the
// caller looked to run.
func (c Client) CloseIdle(ctx context.Context, sessionID string) error {
	return c.close(ctx, sessionID, true)
}

func (c Client) close(ctx context.Context, sessionID string, ifIdle bool) error {
	err := c.closeOn(ctx, sessionID, ifIdle)
	if !(IsCode(err, "not_open") || IsCode(err, "held") || errors.Is(err, ErrNotRunning)) {
		return err
	}
	_, err = c.onRetiring(ctx, err, func(ctx context.Context, host Client) (json.RawMessage, error) {
		return nil, host.closeOn(ctx, sessionID, ifIdle)
	})
	return err
}

func (c Client) closeOn(ctx context.Context, sessionID string, ifIdle bool) error {
	hello, err := c.Hello(ctx)
	if err != nil {
		return err
	}
	if hello.CloseStops {
		return c.call(ctx, "close", map[string]any{"sessionId": sessionID, "ifIdle": ifIdle}, nil)
	}
	// Such a host's close waits out a running turn, so a stop interrupts the
	// turn first. It can answer, or exit without answering, before its Claude
	// has exited, so the close is done once that Claude has.
	params := map[string]string{"sessionId": sessionID}
	listing, err := c.List(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(listing.Sessions, func(s Session) bool { return s.SessionID == sessionID })
	if i < 0 {
		return &Error{Code: "not_open", Message: fmt.Sprintf("Claude session %s is not open in the host", sessionID)}
	}
	listed := listing.Sessions[i]
	pid := listed.PID
	started, err := processStart(pid)
	if err != nil {
		// Claude has already exited.
		started = ""
	}
	if ifIdle {
		// The host cannot refuse a close, so a session it lists as working is
		// refused here; one that starts a turn after this waits it out.
		if listed.State != "idle" || len(listed.Requests) > 0 {
			return &Error{Code: "busy", Message: fmt.Sprintf("Claude session %s is working", sessionID)}
		}
	} else if err := c.call(ctx, "interrupt", params, nil); err != nil && !IsCode(err, "not_open") {
		return err
	}
	var unknown *OutcomeUnknownError
	if err := c.call(ctx, "close", params, nil); err != nil && !IsCode(err, "not_open") && !errors.Is(err, ErrNotRunning) && !errors.As(err, &unknown) {
		return err
	}
	return waitExited(ctx, pid, started)
}

// waitExited waits until the process that had this pid and start time has
// exited; a later process given the same pid does not count.
func waitExited(ctx context.Context, pid int, started string) error {
	for started != "" {
		if now, err := processStart(pid); err != nil || now != started {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Claude %d has not exited: %w", pid, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

// processStart is the process's start time as ps reports it in UTC. It is
// claudepeer.ProcessStart, which this package cannot import.
func processStart(pid int) (string, error) {
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := command.Output()
	return strings.TrimSpace(string(out)), err
}

// sessionCall runs a request for one session on the host that runs it. The
// current host answers not_open for a session it does not run, or held for
// one a retiring host still runs; the retiring hosts are asked then. A host
// that has the session answers for it, whatever its answer.
func (c Client) sessionCall(ctx context.Context, method string, params, out any) error {
	err := c.call(ctx, method, params, out)
	if !(IsCode(err, "not_open") || IsCode(err, "held") || errors.Is(err, ErrNotRunning)) {
		return err
	}
	result, err := c.onRetiring(ctx, err, func(ctx context.Context, host Client) (json.RawMessage, error) {
		var result json.RawMessage
		var target any
		if out != nil {
			target = &result
		}
		err := host.call(ctx, method, params, target)
		return result, err
	})
	if err == nil && out != nil && result != nil {
		return json.Unmarshal(result, out)
	}
	return err
}

// onRetiring runs call on every retiring host at once and returns the answer
// of the one that has the session, so a host that does not answer holds up
// only a session it may run. When none has it, the answer is notOpen.
func (c Client) onRetiring(ctx context.Context, notOpen error, call func(context.Context, Client) (json.RawMessage, error)) (json.RawMessage, error) {
	retiring := c.Retiring()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		result json.RawMessage
		err    error
	}
	answers := make(chan answer, len(retiring))
	for _, host := range retiring {
		go func() {
			result, err := call(ctx, host)
			answers <- answer{result: result, err: err}
		}()
	}
	for range retiring {
		answer := <-answers
		if IsCode(answer.err, "not_open") || errors.Is(answer.err, ErrNotRunning) {
			continue
		}
		return answer.result, answer.err
	}
	return nil, notOpen
}

// List is every session the current and retiring hosts run. A process a
// retiring host runs is the current host's orphan; it is listed once, as the
// retiring host's session. Draining is the current host's. The hosts are asked
// at once, so one that is slow to answer costs the others nothing.
func (c Client) List(ctx context.Context) (Listing, error) {
	hosts := append([]Client{c}, c.Retiring()...)
	listings := make([]Listing, len(hosts))
	errs := make([]error, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = host.call(ctx, "list", struct{}{}, &listings[i])
		}()
	}
	wg.Wait()
	out := listings[0]
	err := errs[0]
	answered := err == nil
	if err != nil {
		out = Listing{}
		if !errors.Is(err, ErrNotRunning) {
			out.Unanswered = append(out.Unanswered, c.Socket)
		}
	}
	served := map[string]bool{}
	for i, host := range hosts[1:] {
		if errs[i+1] != nil {
			out.Unanswered = append(out.Unanswered, host.Socket)
			continue
		}
		answered = true
		for _, session := range listings[i+1].Sessions {
			served[session.SessionID] = true
			out.Sessions = append(out.Sessions, session)
		}
	}
	if !answered {
		return Listing{}, err
	}
	orphans := out.Orphans[:0]
	for _, orphan := range out.Orphans {
		if !served[orphan.SessionID] {
			orphans = append(orphans, orphan)
		}
	}
	out.Orphans = orphans
	return out, nil
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

// SubscribeStart is the host's answer to a subscription: Seq is its latest
// event, and Gap reports that events after the requested one were dropped
// from its buffer, so the caller re-reads state and subscribes after Seq.
type SubscribeStart struct {
	Seq uint64 `json:"seq"`
	Gap bool   `json:"gap"`
}

// Subscribe streams events after the given sequence number until ctx ends or
// the connection drops. On a gap it returns at once without streaming.
func (c Client) Subscribe(ctx context.Context, after uint64, onEvent func(Event)) (start SubscribeStart, err error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return start, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Time{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	payload, _ := json.Marshal(request{ID: 1, Method: "subscribe", Params: map[string]uint64{"after": after}})
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return start, err
	}
	reader := bufio.NewReaderSize(conn, 64<<10)
	line, err := readLine(reader)
	if err != nil {
		return start, err
	}
	var reply response
	if err := json.Unmarshal(line, &reply); err != nil {
		return start, err
	}
	if reply.Error != nil {
		return start, reply.Error
	}
	_ = json.Unmarshal(reply.Result, &start)
	if start.Gap {
		return start, nil
	}
	for {
		line, err := readLine(reader)
		if err != nil {
			if ctx.Err() != nil {
				return start, ctx.Err()
			}
			return start, err
		}
		var message response
		if err := json.Unmarshal(line, &message); err != nil {
			return start, err
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
