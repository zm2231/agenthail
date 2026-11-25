package acphost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/unixjson"
)

var ErrNotRunning = errors.New("the Agenthail ACP host is not running; start the Agenthail daemon")

const replyLimit = 16 << 20

var (
	quickTimeout  = 30 * time.Second
	createTimeout = 3 * time.Minute
	sendTimeout   = 3 * time.Minute
)

type Client struct {
	Home string
	Log  func(format string, args ...any)
}

func SocketPath(home string) string { return filepath.Join(home, ".agenthail", "acp-host.sock") }

func (c Client) sockets() []string {
	main := SocketPath(c.Home)
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(main), "acp-host.*.sock"))
	sort.Strings(matches)
	return append([]string{main}, matches...)
}

func (c Client) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

func (c Client) call(ctx context.Context, socket string, timeout time.Duration, call Call, result any) error {
	started := time.Now()
	var reply Reply
	err := unixjson.Call(ctx, socket, timeout, replyLimit, call, &reply)
	if elapsed := time.Since(started); elapsed > timeout/4 {
		c.logf("ACP host %s took %s (deadline %s)", call.Method, elapsed.Round(time.Millisecond), timeout)
	}
	if err != nil {
		var notSent *unixjson.NotSentError
		if errors.As(err, &notSent) && (errors.Is(err, os.ErrNotExist) || isRefused(err)) {
			return ErrNotRunning
		}
		return err
	}
	if reply.Error != nil {
		return reply.Error
	}
	if result != nil && len(reply.Result) > 0 {
		return json.Unmarshal(reply.Result, result)
	}
	return nil
}

func isRefused(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func (c Client) Hello(ctx context.Context) (Hello, error) {
	var hello Hello
	err := c.call(ctx, SocketPath(c.Home), 2*time.Second, Call{Method: "hello"}, &hello)
	return hello, err
}

func (c Client) Drain(ctx context.Context) error {
	return c.call(ctx, SocketPath(c.Home), quickTimeout, Call{Method: "drain"}, nil)
}

func (c Client) Held(ctx context.Context) ([]State, error) {
	var all []State
	reached := false
	var lastErr error
	for _, socket := range c.sockets() {
		var held HeldResult
		if err := c.call(ctx, socket, quickTimeout, Call{Method: "held"}, &held); err != nil {
			lastErr = err
			continue
		}
		reached = true
		all = append(all, held.Sessions...)
	}
	if !reached {
		return nil, lastErr
	}
	return all, nil
}

func (c Client) holder(ctx context.Context, sessionID string) string {
	for _, socket := range c.sockets() {
		var held HeldResult
		if c.call(ctx, socket, quickTimeout, Call{Method: "held"}, &held) != nil {
			continue
		}
		for _, state := range held.Sessions {
			if state.SessionID == sessionID {
				return socket
			}
		}
	}
	return SocketPath(c.Home)
}

func (c Client) State(ctx context.Context, sessionID string) (State, bool, error) {
	states, err := c.Held(ctx)
	if err != nil {
		return State{}, false, err
	}
	for _, state := range states {
		if state.SessionID == sessionID {
			return state, true, nil
		}
	}
	return State{}, false, nil
}

func (c Client) keyed(ctx context.Context, socket string, timeout time.Duration, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	call := Call{Method: method, Key: uuid.NewString(), Params: raw}
	err = c.call(ctx, socket, timeout, call, result)
	if err == nil || errors.Is(err, ErrNotRunning) {
		return err
	}
	var callErr *CallError
	if errors.As(err, &callErr) {
		return err
	}
	lookup, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var outcome Outcome
	if lookupErr := c.call(lookup, socket, timeout, Call{Method: "outcome", Params: mustJSON(OutcomeParams{Key: call.Key})}, &outcome); lookupErr != nil {
		return surface.DeliveryOutcomeUnknown(fmt.Errorf("the ACP host did not confirm %s: %w", method, err))
	}
	switch outcome.State {
	case OutcomeDone:
		if result != nil && len(outcome.Result) > 0 {
			return json.Unmarshal(outcome.Result, result)
		}
		return nil
	case OutcomeFailed:
		if outcome.Error != nil {
			return outcome.Error
		}
		return fmt.Errorf("the ACP host did not %s", method)
	}
	return surface.DeliveryOutcomeUnknown(fmt.Errorf("the ACP host is still working on %s: %w", method, err))
}

func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func (c Client) Create(ctx context.Context, params CreateParams) (State, error) {
	var state State
	err := c.keyed(ctx, SocketPath(c.Home), createTimeout, "create", params, &state)
	return state, err
}

func (c Client) Send(ctx context.Context, params SendParams) (SendResult, error) {
	var result SendResult
	err := c.keyed(ctx, c.holder(ctx, params.SessionID), sendTimeout, "send", params, &result)
	return result, err
}

func (c Client) Cancel(ctx context.Context, sessionID string) error {
	return c.keyed(ctx, c.holder(ctx, sessionID), quickTimeout, "cancel", SessionParams{SessionID: sessionID}, nil)
}

func (c Client) Respond(ctx context.Context, params RespondParams) (RespondResult, error) {
	var result RespondResult
	err := c.keyed(ctx, c.holder(ctx, params.SessionID), quickTimeout, "respond", params, &result)
	return result, err
}

func (c Client) SetOption(ctx context.Context, params OptionParams) (State, error) {
	var state State
	err := c.keyed(ctx, c.holder(ctx, params.SessionID), sendTimeout, "setOption", params, &state)
	return state, err
}

func (c Client) Close(ctx context.Context, sessionID string) error {
	return c.keyed(ctx, c.holder(ctx, sessionID), quickTimeout, "close", SessionParams{SessionID: sessionID}, nil)
}

func (c Client) Subscribe(ctx context.Context, onEvent func(Event)) error {
	sockets := c.sockets()
	done := make(chan error, len(sockets))
	for _, socket := range sockets {
		go func(socket string) { done <- c.follow(ctx, socket, onEvent) }(socket)
	}
	var first error
	for range sockets {
		if err := <-done; err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (c Client) follow(ctx context.Context, socket string, onEvent func(Event)) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return ErrNotRunning
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(Call{Method: "subscribe"}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.SessionID != "" {
			onEvent(event)
		}
	}
	return ctx.Err()
}
