package coordinator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// Service delivers to the coordinator so that a message always lands: it is
// delivered or queued for the running conversation, or it starts a new one
// from the saved spec when the old conversation is gone.
type Service struct {
	Registry     *registry.Registry
	Adapters     surface.Adapters
	Dispatcher   delivery.Dispatcher
	// ClaudeRoot is the root of a lead conversation that replaces previous, which is empty for a first start.
	ClaudeRoot   func(previous string) (string, error)
	LeadsChanged func()
	// Replaced runs after a handover is recorded, so its notices go out.
	Replaced func()
	Now      func() time.Time
}

// GoneGrace covers a conversation started moments ago that has not yet written its transcript or process record.
const GoneGrace = 2 * time.Minute

// RestartedByDaemon is who replaced a coordinator for a queued message with no sender.
const RestartedByDaemon = "daemon"

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type Outcome struct {
	Receipt *delivery.Receipt `json:"receipt"`
	Session *surface.Session  `json:"session"`
	Started bool              `json:"started"`
	// Previous is the conversation a start replaced, "" when none existed.
	Previous  string `json:"previous,omitempty"`
	Reason    string `json:"reason,omitempty"`
	ChangedBy string `json:"changedBy,omitempty"`
	// HistoryID is the coordinator_history row of the handover; it keys its notices.
	HistoryID int64 `json:"historyId,omitempty"`
}

// Of returns the spec of the project sessionID coordinates, as its current
// conversation or one a restart replaced.
func (s Service) Of(sessionID string) (registry.CoordinatorSpec, bool) {
	if s.Registry == nil {
		return registry.CoordinatorSpec{}, false
	}
	spec, found, err := s.Registry.CoordinatorOfSession(sessionID)
	return spec, err == nil && found
}

// Named returns the coordinator target is @<name> of. A bare name stays a
// fragment like any other target.
func (s Service) Named(target string) (registry.CoordinatorSpec, bool) {
	name, ok := strings.CutPrefix(target, "@")
	if s.Registry == nil || !ok {
		return registry.CoordinatorSpec{}, false
	}
	spec, err := s.Registry.CoordinatorNamed(name)
	return spec, err == nil
}

// ReplacedBy returns the coordinator's current session when sessionID is a
// conversation a restart replaced.
func (s Service) ReplacedBy(sessionID string) (string, bool) {
	spec, ok := s.Of(sessionID)
	if !ok {
		return "", false
	}
	current, err := s.Registry.CoordinatorSessionID(spec)
	if err != nil || current == "" || current == sessionID {
		return "", false
	}
	return current, true
}

// Started reports whether the coordinator's name points at a session.
func (s Service) Started(spec registry.CoordinatorSpec) bool {
	id, err := s.Registry.CoordinatorSessionID(spec)
	return err == nil && id != ""
}

func (s Service) Deliver(ctx context.Context, spec registry.CoordinatorSpec, message string, options surface.SendOptions) (*Outcome, error) {
	return s.deliver(ctx, spec, message, "", options, true)
}

// DeliverWithoutQueue refuses to wait for a busy or unavailable coordinator,
// but still starts one whose conversation is gone.
func (s Service) DeliverWithoutQueue(ctx context.Context, spec registry.CoordinatorSpec, message string, options surface.SendOptions) (*Outcome, error) {
	return s.deliver(ctx, spec, message, "", options, false)
}

// Sender delivers to a session as delivery.Dispatcher does, except that a
// message for the coordinator's current conversation, or one RouteOf moves
// from a conversation a restart replaced, goes through the coordinator. A
// send then updates session to the conversation that took the message.
type Sender struct{ Service }

func (s Sender) DeliverWithOptions(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions) (*delivery.Receipt, error) {
	return s.send(ctx, adapter, session, message, deliveryKey, options, true)
}

func (s Sender) DeliverWithoutQueue(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions) (*delivery.Receipt, error) {
	return s.send(ctx, adapter, session, message, deliveryKey, options, false)
}

func (s Sender) send(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions, allowQueue bool) (*delivery.Receipt, error) {
	spec, coordinated := s.Of(session.ID)
	if !coordinated {
		return s.dispatch(ctx, adapter, session, message, deliveryKey, options, allowQueue)
	}
	route, err := s.RouteOf(ctx, session.ID, FromAgent(options.Author, options.SourceSessionID))
	if err != nil {
		if !allowQueue {
			return nil, err
		}
		return s.Hold(session, message, options, err)
	}
	if route.Replaced && !route.Moved {
		return s.dispatch(ctx, adapter, session, message, deliveryKey, options, allowQueue)
	}
	outcome, err := s.deliver(ctx, spec, message, deliveryKey, options, allowQueue)
	if err != nil {
		return nil, err
	}
	*session = *outcome.Session
	route.Annotate(outcome.Receipt, spec.Name)
	return outcome.Receipt, nil
}

func (s Sender) dispatch(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions, allowQueue bool) (*delivery.Receipt, error) {
	if allowQueue {
		return s.Dispatcher.DeliverWithOptions(ctx, adapter, session, message, deliveryKey, options)
	}
	return s.Dispatcher.DeliverWithoutQueue(ctx, adapter, session, message, deliveryKey, options)
}

// Canonical returns the coordinator's current conversation when sessionID is
// one a restart replaced, and sessionID otherwise.
func (s Service) Canonical(sessionID string) string {
	if current, replaced := s.ReplacedBy(sessionID); replaced {
		return current
	}
	return sessionID
}

func (s Service) deliver(ctx context.Context, spec registry.CoordinatorSpec, message, deliveryKey string, options surface.SendOptions, allowQueue bool) (*Outcome, error) {
	session, adapter, reason, err := s.current(ctx, spec)
	if err != nil {
		return s.queueUnavailable(session, message, options, allowQueue, err)
	}
	if reason == "" {
		return s.deliverTo(ctx, adapter, session, message, deliveryKey, options, allowQueue)
	}
	unlock, err := lock(ctx, s.Registry.CoordinatorLockPath(spec.ProjectID))
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer unlock()
	if spec, err = s.Registry.Coordinator(spec.ProjectID); err != nil {
		return nil, err
	}
	session, adapter, reason, err = s.current(ctx, spec)
	if err != nil {
		return s.queueUnavailable(session, message, options, allowQueue, err)
	}
	if reason == "" {
		return s.deliverTo(ctx, adapter, session, message, deliveryKey, options, allowQueue)
	}
	previous := ""
	if session != nil {
		previous = session.ID
	}
	root, err := s.startRoot(spec, previous)
	if err != nil {
		return s.queueUnavailable(session, message, options, allowQueue, err)
	}
	// A new conversation's first turn is text, so attached files are named
	// by their stored paths.
	composed, _ := options.Uploads.Compose(message, false)
	return s.start(ctx, spec, previous, root, reason, options.Sender(), composed, false)
}

func (s Service) startRoot(spec registry.CoordinatorSpec, previous string) (string, error) {
	if spec.Surface != string(surface.KindClaude) || s.ClaudeRoot == nil {
		return "", nil
	}
	root, err := s.ClaudeRoot(previous)
	if err != nil {
		return "", surface.DeliveryUnavailable(err)
	}
	return root, nil
}

// Restart starts a new conversation for a message the outbox holds for
// session when that conversation is gone. It reports false when session is
// not a coordinator or can still take the message; a failed check is
// handled, so the message is retried rather than sent to a blank resume.
func (s Service) Restart(ctx context.Context, session *surface.Session, message string) (*Outcome, bool, error) {
	spec, ok := s.Of(session.ID)
	if !ok {
		return nil, false, nil
	}
	if _, _, reason, err := s.current(ctx, spec); err != nil || reason == "" {
		return nil, err != nil, err
	}
	unlock, err := lock(ctx, s.Registry.CoordinatorLockPath(spec.ProjectID))
	if err != nil {
		return nil, true, surface.DeliveryUnavailable(err)
	}
	defer unlock()
	if spec, err = s.Registry.Coordinator(spec.ProjectID); errors.Is(err, registry.ErrNoCoordinator) {
		return nil, false, nil
	} else if err != nil {
		return nil, true, err
	}
	current, _, reason, err := s.current(ctx, spec)
	if err != nil {
		return nil, true, err
	}
	if reason == "" || current == nil || current.ID != session.ID {
		return nil, false, nil
	}
	changedBy := surface.SourceSessionID(ctx)
	if changedBy == "" {
		changedBy = RestartedByDaemon
	}
	root, err := s.startRoot(spec, current.ID)
	if err != nil {
		return nil, true, err
	}
	outcome, err := s.start(ctx, spec, current.ID, root, reason, changedBy, message, true)
	return outcome, true, err
}

// current returns the coordinator's session and a reason it must start again,
// "" when the session can take a message. When the check fails it still
// returns the session, so the message can wait for it.
func (s Service) current(ctx context.Context, spec registry.CoordinatorSpec) (*surface.Session, surface.Surface, string, error) {
	id, err := s.Registry.CoordinatorSessionID(spec)
	if err != nil {
		return nil, nil, "", err
	}
	if id == "" {
		return nil, nil, fmt.Sprintf("no conversation was named @%s", spec.Name), nil
	}
	session, err := s.Registry.Session(id)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read coordinator session %s: %w", id, err)
	}
	adapter, err := s.Adapters.For(session)
	if err != nil {
		return nil, nil, "", err
	}
	if keeper, ok := adapter.(surface.ConversationKeeper); ok {
		state, since, err := s.conversationState(ctx, keeper, session)
		switch state {
		case surface.ConversationGone:
			return session, adapter, fmt.Sprintf("conversation %s no longer exists (nothing holds it and it was last seen %s ago)", id, since.Round(time.Second)), nil
		case surface.ConversationUnknown:
			return session, adapter, "", surface.DeliveryUnavailable(fmt.Errorf("check coordinator conversation %s: %w", id, err))
		}
	}
	return session, adapter, "", nil
}

func (s Service) conversationState(ctx context.Context, keeper surface.ConversationKeeper, session *surface.Session) (surface.ConversationState, time.Duration, error) {
	state, err := keeper.ConversationState(ctx, session)
	if state == surface.ConversationUnknown && err == nil {
		err = errors.New("its state could not be read")
	}
	if state != surface.ConversationGone {
		return state, 0, err
	}
	seen, err := s.Registry.SessionLastSeen(session.ID)
	if err != nil {
		return surface.ConversationUnknown, 0, fmt.Errorf("read when it was last seen: %w", err)
	}
	since := s.now().Sub(seen)
	if since < GoneGrace {
		return surface.ConversationUnknown, since, fmt.Errorf("nothing of it was found, but it was seen %s ago, too recently to count as gone", since.Round(time.Second))
	}
	return surface.ConversationGone, since, nil
}

// deliverTo steers a busy coordinator unless the sender chose otherwise.
func (s Service) deliverTo(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions, allowQueue bool) (*Outcome, error) {
	if options.BusyDelivery == "" {
		options.BusyDelivery = string(deliverypolicy.Steer)
	}
	var receipt *delivery.Receipt
	var err error
	if allowQueue {
		receipt, err = s.Dispatcher.DeliverWithOptions(ctx, adapter, session, message, deliveryKey, options)
	} else {
		receipt, err = s.Dispatcher.DeliverWithoutQueue(ctx, adapter, session, message, deliveryKey, options)
	}
	if err != nil {
		return s.queueUnavailable(session, message, options, allowQueue, err)
	}
	return &Outcome{Receipt: receipt, Session: session}, nil
}

// Hold queues only for a cause that is an unavailable delivery; any other
// cause is returned.
func (s Service) Hold(session *surface.Session, message string, options surface.SendOptions, cause error) (*delivery.Receipt, error) {
	outcome, err := s.queueUnavailable(session, message, options, true, cause)
	if err != nil {
		return nil, err
	}
	return outcome.Receipt, nil
}

// queueUnavailable queues a message for a coordinator that cannot take it for
// now, so it is retried rather than lost or replaced by a new conversation.
func (s Service) queueUnavailable(session *surface.Session, message string, options surface.SendOptions, allowQueue bool, cause error) (*Outcome, error) {
	if session == nil || !allowQueue || !surface.IsDeliveryUnavailable(cause) {
		return nil, cause
	}
	if options.BusyDelivery == "" {
		options.BusyDelivery = string(deliverypolicy.Steer)
	}
	queueID, deliveryID, err := s.Registry.QueueDeliveryWithIntent(session.ID, message, "", options)
	if err != nil {
		return nil, errors.Join(cause, err)
	}
	target := session.Name
	if target == "" {
		target = session.ID
	}
	receipt := &delivery.Receipt{Evidence: surface.EvidenceQueued, Status: string(registry.DeliveryIntentQueued), SessionID: session.ID, QueueID: queueID, DeliveryID: deliveryID, MessageID: delivery.Dispatcher{Registry: s.Registry}.QueuedMessageID(deliveryID, session.ID), Detail: fmt.Sprintf("Queued for %s, which cannot take it yet: %s", target, cause)}
	return &Outcome{Receipt: receipt, Session: session}, nil
}

// A queued message already owns its delivery record, so a start that serves it records none of its own.
func (s Service) start(ctx context.Context, spec registry.CoordinatorSpec, previous, root, reason, sender, message string, queued bool) (*Outcome, error) {
	adapter := s.Adapters.ForSurface(surface.SurfaceKind(spec.Surface))
	starter, ok := adapter.(surface.SessionStarter)
	if adapter == nil || !ok {
		return nil, surface.DeliveryTerminal(fmt.Errorf("%s cannot start a coordinator conversation", spec.Surface), surface.DeliveryInvalidRequest)
	}
	first := FirstTurn(spec, reason, message)
	options := surface.SessionStartOptions{Message: first, Cwd: spec.Cwd, Model: spec.Model}
	if spec.Surface == string(surface.KindClaude) {
		options.Name, options.PermissionMode, options.ConfigRoot = spec.Name, spec.PermissionMode, root
	}
	messageUUID := surface.MessageUUID(ctx)
	if !queued || messageUUID == "" {
		messageUUID = uuid.NewString()
	}
	settle := s.Dispatcher.CapturePersonStart(sender, messageUUID, first, message)
	var readyErr error
	ready := func(started surface.Session) error {
		readyErr = s.Registry.RecordStartingSession(started, registry.AliasReservation{}, registry.SessionOrigin{SessionID: started.ID, Via: registry.OriginCoordinator})
		return readyErr
	}
	session, sent, startErr := starter.StartSession(surface.WithSessionReady(surface.WithSourceSessionID(surface.WithMessageUUID(ctx, messageUUID), sender), ready), options)
	if session != nil && readyErr != nil {
		settle("")
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("coordinator conversation %s started but could not be recorded before its first turn, which was not sent: %w", session.ID, readyErr))
	}
	if session == nil {
		settle("")
	} else {
		settle(session.ID)
	}
	if session == nil {
		if startErr == nil {
			startErr = errors.New("session starter returned no session")
		}
		_ = s.Registry.RecordHistory(registry.HistoryEntry{Kind: failureKind(startErr), SessionID: previous, Message: message, Error: fmt.Sprintf("start coordinator: %s", startErr)})
		return nil, startErr
	}
	if s.LeadsChanged != nil {
		defer s.LeadsChanged()
	}
	if err := s.Registry.RegisterSession(*session); err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("coordinator conversation %s started but could not be registered: %w", session.ID, err))
	}
	if err := s.Registry.RecordSessionOrigin(registry.SessionOrigin{SessionID: session.ID, Via: registry.OriginCoordinator}); err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("coordinator conversation %s started but could not be recorded as the coordinator's: %w", session.ID, err))
	}
	var intent *registry.DeliveryIntent
	if startErr != nil {
		if !queued {
			recorded, intentErr := s.Registry.RecordSessionCreationIntent(session.ID, sender, messageUUID, message)
			if intentErr != nil {
				return nil, errors.Join(startErr, intentErr)
			}
			intent = recorded
		}
		if !surface.IsDeliveryOutcomeUnknown(startErr) {
			recorded := false
			if intent != nil {
				recorded, _ = s.Registry.FailDeliveryIntentWithNotice(intent.ID, startErr.Error())
			}
			_ = s.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: startErr.Error()})
			if !surface.IsDeliveryTerminal(startErr) {
				startErr = surface.DeliveryTerminal(startErr)
			}
			if recorded {
				startErr = delivery.FailedDeliveryError{DeliveryID: intent.ID, Err: startErr}
			}
			return nil, startErr
		}
	}
	historyID, err := s.Registry.HandOverCoordinator(spec, previous, session.ID, registry.CoordinatorHandover{ChangedBy: sender, Reason: "gone: " + reason, NotifySender: queued})
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("coordinator conversation %s started but @%s could not point at it: %w", session.ID, spec.Name, err))
	}
	if previous != "" && s.Replaced != nil {
		defer s.Replaced()
	}
	outcome := &Outcome{Session: session, Started: true, Previous: previous, Reason: reason, ChangedBy: sender, HistoryID: historyID}
	if startErr != nil {
		_ = s.Registry.RecordHistory(registry.HistoryEntry{Kind: "submitted", SessionID: session.ID, Message: message, Error: startErr.Error()})
		outcome.Receipt = &delivery.Receipt{Evidence: surface.EvidenceSubmitted, Status: string(registry.DeliveryIntentSubmitted), SessionID: session.ID, Started: true, Detail: startedDetail(spec, previous, reason)}
		if intent != nil {
			outcome.Receipt.DeliveryID, outcome.Receipt.MessageID = intent.ID, intent.UUID
		}
		return outcome, nil
	}
	turn := ""
	if sent != nil {
		turn = sent.UUID
	}
	_ = s.Registry.RecordHistory(registry.HistoryEntry{Kind: "sent", SessionID: session.ID, Message: message, Result: turn})
	outcome.Receipt = &delivery.Receipt{Evidence: surface.EvidenceDelivered, Status: string(registry.DeliveryIntentSent), SessionID: session.ID, TurnID: turn, Started: true, Detail: startedDetail(spec, previous, reason)}
	return outcome, nil
}

// FirstTurn is what a new coordinator conversation receives: its standing
// instructions, why it started, then the message that started it.
func FirstTurn(spec registry.CoordinatorSpec, reason, message string) string {
	var b strings.Builder
	if instructions := strings.TrimSpace(spec.Instructions); instructions != "" {
		b.WriteString(instructions)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "[Agenthail started this conversation as coordinator @%s because %s.]\n\n", spec.Name, reason)
	b.WriteString(message)
	return b.String()
}

func startedDetail(spec registry.CoordinatorSpec, previous, reason string) string {
	if previous == "" {
		return fmt.Sprintf("Started coordinator @%s (%s).", spec.Name, reason)
	}
	return fmt.Sprintf("Started a new conversation for coordinator @%s: %s.", spec.Name, reason)
}

func failureKind(err error) string {
	if surface.IsDeliveryOutcomeUnknown(err) {
		return "unknown"
	}
	return "failed"
}

func lock(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open coordinator lock: %w", err)
	}
	for {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("wait for coordinator start: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
