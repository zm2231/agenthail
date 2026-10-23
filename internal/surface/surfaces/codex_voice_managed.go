package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

const (
	voiceLinkEventLimit = 1000
	voiceToolRequestTTL = 10 * time.Minute
)

// voiceLink is the app-server connection one managed voice call owns. It
// subscribes to the operator thread, carries the call's requests, keeps the
// thread's voice events in sequence for the service to poll, and answers the
// tool calls Codex sends on it.
type voiceLink struct {
	client   *managedCodexClient
	threadID string
	source   string
	account  map[string]any
	// accountRead is false when Codex could not say which login it holds.
	accountRead bool

	mu       sync.Mutex
	events   []voice.Event
	sequence int64
	requests map[string]voiceToolRequest
	order    []string
	closed   bool
	// followed closes when the link stops reading its connection.
	followed chan struct{}
	// appended closes, and is replaced, when the link takes new events.
	appended chan struct{}
}

type voiceToolRequest struct {
	id       json.RawMessage
	threadID string
	at       time.Time
}

func (v *CodexVoice) openVoiceLink(ctx context.Context, session *surface.Session) (*voiceLink, error) {
	client, err := v.Codex.openManaged(ctx)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	managed, ok := client.(*managedCodexClient)
	if !ok {
		_ = client.Close()
		return nil, surface.DeliveryUnavailable(errors.New("managed voice needs an app-server connection"))
	}
	resumed, err := managed.Request(ctx, "thread/resume", codexResumeParams(session), 10*time.Second)
	if err != nil {
		_ = managed.Close()
		return nil, classifyCodexWrite(fmt.Errorf("thread/resume: %w", err))
	}
	result, _ := resumed["result"].(map[string]any)
	if thread, _ := result["thread"].(map[string]any); str(thread, "name") != voiceOperatorName {
		if err := nameVoiceOperator(ctx, managed, session.ID); err != nil {
			_ = managed.Close()
			return nil, classifyCodexWrite(fmt.Errorf("name the voice operator: %w", err))
		}
	}
	link := &voiceLink{client: managed, threadID: session.ID, source: "managed:" + uuid.NewString(), requests: map[string]voiceToolRequest{}, followed: make(chan struct{}), appended: make(chan struct{})}
	if response, err := managed.Request(ctx, "account/read", map[string]any{}, 5*time.Second); err == nil {
		result, _ := response["result"].(map[string]any)
		link.account, _ = result["account"].(map[string]any)
		link.accountRead = true
	}
	go link.follow()
	return link, nil
}

func (l *voiceLink) follow() {
	defer close(l.followed)
	for {
		select {
		case <-l.client.Notified():
		case <-l.client.closed:
			l.collect()
			return
		}
		l.collect()
	}
}

func (l *voiceLink) collect() {
	events := l.client.DrainNotifications()
	l.mu.Lock()
	defer l.mu.Unlock()
	taken := l.sequence
	defer func() {
		if l.sequence != taken {
			close(l.appended)
			l.appended = make(chan struct{})
		}
	}()
	for _, event := range events {
		if threadID, _ := event.Params["threadId"].(string); threadID != l.threadID || !voiceEventMethod(event) {
			continue
		}
		params := event.Params
		if len(event.RequestID) > 0 {
			key := string(event.RequestID)
			now := time.Now()
			for len(l.order) > 0 {
				oldest, ok := l.requests[l.order[0]]
				if ok && now.Sub(oldest.at) < voiceToolRequestTTL && len(l.order) < 4096 {
					break
				}
				delete(l.requests, l.order[0])
				l.order = l.order[1:]
			}
			l.requests[key] = voiceToolRequest{id: event.RequestID, threadID: l.threadID, at: now}
			l.order = append(l.order, key)
			params = make(map[string]any, len(event.Params)+1)
			for name, value := range event.Params {
				params[name] = value
			}
			params["requestId"] = key
		}
		l.sequence++
		l.events = append(l.events, voice.Event{Sequence: l.sequence, Method: event.Method, Params: params})
	}
	if len(l.events) > voiceLinkEventLimit {
		l.events = l.events[len(l.events)-voiceLinkEventLimit:]
	}
}

// voiceEventMethod matches the events the Desktop route forwards.
func voiceEventMethod(event codexEvent) bool {
	switch {
	case event.Method == "thread/realtime/outputAudio/delta":
		return false
	case strings.HasPrefix(event.Method, "thread/realtime/"), event.Method == "item/tool/call", event.Method == "turn/started":
		return true
	case event.Method == "item/completed":
		item, _ := event.Params["item"].(map[string]any)
		return str(item, "type") == "agentMessage" && str(item, "phase") == "final_answer"
	}
	return false
}

// poll returns the events after the cursor as soon as there are any, or an
// empty batch once wait passes, the link stops following or ctx ends.
func (l *voiceLink) poll(ctx context.Context, after voice.Cursor, wait time.Duration) (voice.Batch, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		batch, appended := l.read(after)
		if len(batch.Events) > 0 || batch.Lost || wait <= 0 {
			return batch, nil
		}
		select {
		case <-appended:
		case <-l.followed:
			batch, _ = l.read(after)
			return batch, nil
		case <-timer.C:
			return batch, nil
		case <-ctx.Done():
			return batch, ctx.Err()
		}
	}
}

func (l *voiceLink) read(after voice.Cursor) (voice.Batch, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if after.Source != l.source {
		return voice.Batch{Cursor: voice.Cursor{Source: l.source, Position: l.sequence}, Lost: true}, l.appended
	}
	lost := after.Position > l.sequence || (len(l.events) > 0 && l.events[0].Sequence > after.Position+1)
	var events []voice.Event
	for _, event := range l.events {
		if event.Sequence <= after.Position {
			continue
		}
		if requestID, _ := event.Params["requestId"].(string); event.Method == "item/tool/call" && l.requests[requestID].id == nil {
			continue
		}
		events = append(events, event)
	}
	if l.client.failed() && !l.closed {
		lost = true
	}
	return voice.Batch{Cursor: voice.Cursor{Source: l.source, Position: l.sequence}, Events: events, Lost: lost}, l.appended
}

func (l *voiceLink) respond(requestID, threadID string, success bool, text string) error {
	l.mu.Lock()
	request, ok := l.requests[requestID]
	l.mu.Unlock()
	if !ok {
		return errors.New("dynamic tool request is no longer available")
	}
	if request.threadID != threadID {
		return errors.New("dynamic tool response thread does not match its request")
	}
	if err := l.client.Respond(request.id, map[string]any{
		"success": success, "contentItems": []any{map[string]any{"type": "inputText", "text": text}},
	}); err != nil {
		return err
	}
	l.mu.Lock()
	delete(l.requests, requestID)
	l.mu.Unlock()
	return nil
}

func (l *voiceLink) close() {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	_ = l.client.Close()
}

func (l *voiceLink) startRefusal(err error) error {
	reason := err.Error()
	var rpc *codexRPCError
	if errors.As(err, &rpc) {
		reason = rpc.Message
	}
	return &voice.RejectedError{Reason: strings.TrimRight(reason, ". ") + ". " + realtimeRemedy(l.account)}
}

func (l *voiceLink) realtimeLoginRefusal() error {
	if !l.accountRead {
		return nil
	}
	switch str(l.account, "type") {
	case "chatgpt", "apiKey":
		return nil
	case "":
		return &voice.RejectedError{Reason: "Codex on this host is not signed in, and voice needs its login. " + realtimeRemedy(nil)}
	default:
		return &voice.RejectedError{Reason: "Codex on this host is signed in with " + str(l.account, "type") + ", which has no realtime voice. " + realtimeRemedy(l.account)}
	}
}

func realtimeRemedy(account map[string]any) string {
	switch str(account, "type") {
	case "apiKey":
		return "Codex here uses an API key, so voice goes to the OpenAI Realtime API with that key: use a key with Realtime API access, or sign in with ChatGPT by running 'codex login' on this host."
	case "chatgpt":
		return "Codex here is signed in with ChatGPT, so voice goes to ChatGPT's realtime service: if the login expired, run 'codex login' on this host again."
	}
	return "Run 'codex login' on this host to sign in with ChatGPT, or configure Codex with an OpenAI API key that has Realtime API access."
}
