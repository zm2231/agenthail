package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type Zen struct{}

func NewZen() *Zen { return &Zen{} }

func (*Zen) Name() surface.SurfaceKind { return surface.KindZen }

func (*Zen) List(context.Context) ([]surface.Session, error) { return []surface.Session{}, nil }

func (*Zen) Resolve(context.Context, string) (*surface.Session, error) {
	return nil, surface.ErrUnsupported
}

func (*Zen) Observe(_ context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	return &surface.TurnObservation{Status: session.Status}, nil
}

func (z *Zen) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	return z.command(ctx, session, "interact", message)
}

func (z *Zen) Reply(context.Context, *surface.Session, int) (*surface.ReplyResult, error) {
	return nil, surface.ErrUnsupported
}

func (*Zen) Tail(context.Context, *surface.Session, int) ([]surface.Exchange, error) {
	return []surface.Exchange{}, nil
}

func (*Zen) Stream(context.Context, *surface.Session, string, func(surface.StreamEvent), time.Duration) error {
	return surface.ErrUnsupported
}

func (*Zen) GoalSet(context.Context, *surface.Session, string) error { return surface.ErrUnsupported }
func (*Zen) GoalClear(context.Context, *surface.Session) error       { return surface.ErrUnsupported }
func (*Zen) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return nil, surface.ErrUnsupported
}
func (*Zen) Compact(context.Context, *surface.Session) error { return surface.ErrUnsupported }
func (*Zen) Model(context.Context, *surface.Session, string) (string, error) {
	return "", surface.ErrUnsupported
}

func (z *Zen) Interrupt(ctx context.Context, session *surface.Session) error {
	_, err := z.command(ctx, session, "interrupt", "")
	return err
}

func (z *Zen) Steer(ctx context.Context, session *surface.Session, message string) error {
	_, err := z.command(ctx, session, "steer", message)
	return err
}

func (*Zen) Capabilities() surface.Capabilities {
	return surface.Capabilities{Send: true, Interrupt: true, Steer: true}
}

func (*Zen) EnsureWritable(_ context.Context, session *surface.Session) error {
	if strings.TrimSpace(session.Transport) == "" {
		return fmt.Errorf("ZEN session has no control socket")
	}
	return nil
}

func (*Zen) command(ctx context.Context, session *surface.Session, action, prompt string) (*surface.SendResult, error) {
	transport, err := url.Parse(session.Transport)
	if err != nil || transport.Scheme != "unix" || transport.Path == "" {
		return nil, fmt.Errorf("ZEN session has no control socket")
	}
	controller := transport.Query().Get("controller")
	generation, parseErr := strconv.Atoi(transport.Query().Get("generation"))
	if controller == "" || parseErr != nil || generation < 1 {
		return nil, fmt.Errorf("ZEN session has no valid control lease")
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", transport.Path)
	if err != nil {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("ZEN control socket: %w", err))
	}
	defer connection.Close()
	requestID := fmt.Sprintf("agenthail:%d", time.Now().UnixNano())
	request := map[string]any{"id": requestID, "action": action, "controller": controller, "generation": generation, "idempotencyKey": requestID}
	if prompt != "" {
		request["prompt"] = prompt
	}
	if sourceSessionID := surface.SourceSessionID(ctx); sourceSessionID != "" {
		request["sourceSessionId"] = sourceSessionID
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err = connection.Write(append(encoded, '\n')); err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	line, err := bufio.NewReader(connection).ReadBytes('\n')
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, err
	}
	if !response.OK {
		return nil, surface.DeliveryTerminal(fmt.Errorf("ZEN control rejected: %s", response.Error), surface.DeliveryOwnershipConflict)
	}
	return &surface.SendResult{UUID: request["id"].(string), Accepted: true}, nil
}
