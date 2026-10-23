package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

// CodexVoice runs voice calls on Codex's managed app-server, and on Codex
// Desktop only for an operator Desktop has loaded.
type CodexVoice struct {
	Codex *Codex
	mu    sync.Mutex
	links map[string]*voiceLink
}

const voiceOperatorName = "Agenthail voice orchestrator"

func (v *CodexVoice) Create(ctx context.Context, cwd, instructions string) (*surface.Session, error) {
	if v.Codex.managed {
		return v.createManaged(ctx, cwd, instructions)
	}
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	client, err := v.Codex.openDesktop(ctx)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer client.Close()
	return createVoiceOperator(ctx, client, cwd, instructions)
}

func createVoiceOperator(ctx context.Context, client codexClient, cwd, instructions string) (*surface.Session, error) {
	response, err := client.Request(ctx, "thread/start", map[string]any{
		"cwd": cwd, "developerInstructions": instructions, "ephemeral": false,
		"threadSource": "agenthail", "serviceName": "agenthail-voice", "dynamicTools": voice.DynamicTools(),
	}, 15*time.Second)
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	id := str(thread, "id")
	if id == "" {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Codex did not return an operator thread ID"))
	}
	session := &surface.Session{ID: id, Name: voiceOperatorName, Cwd: cwd,
		Surface: surface.KindCodex, Source: "agenthail",
		HasLocal: true, Status: surface.StatusIdle, Transcript: str(thread, "path"), LastActive: time.Now()}
	setCodexTransport(session, codexTransportDesktop)
	return session, nil
}

func (v *CodexVoice) createManaged(ctx context.Context, cwd, instructions string) (*surface.Session, error) {
	client, err := v.Codex.openManaged(ctx)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer client.Close()
	session, err := createVoiceOperator(ctx, client, cwd, instructions)
	if err != nil {
		return nil, err
	}
	setCodexTransport(session, codexTransportManaged)
	// A refused name is set again when the next call opens its link.
	_ = nameVoiceOperator(ctx, client, session.ID)
	return session, nil
}

// nameVoiceOperator gives the operator thread its fixed name. A named thread
// keeps its name; Codex would otherwise title it from the first thing said on
// a call.
func nameVoiceOperator(ctx context.Context, client codexClient, threadID string) error {
	_, err := client.Request(ctx, "thread/name/set", map[string]any{"threadId": threadID, "name": voiceOperatorName}, 5*time.Second)
	return err
}

func (v *CodexVoice) link(threadID string) *voiceLink {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.links[threadID]
}

func (v *CodexVoice) dropLink(threadID string, link *voiceLink) {
	v.mu.Lock()
	if v.links[threadID] == link {
		delete(v.links, threadID)
	}
	v.mu.Unlock()
	link.close()
}

// voiceRoute is Desktop only while Desktop has the operator loaded.
func (v *CodexVoice) voiceRoute(ctx context.Context, s *surface.Session) (string, error) {
	if !v.Codex.managed {
		return codexTransportDesktop, nil
	}
	runtimes := v.Codex.openRuntimes(ctx)
	defer runtimes.Close()
	if err := runtimes.ownershipUnknown(); err != nil {
		return "", err
	}
	if runtimes.desktopLoaded[s.ID] {
		return codexTransportDesktop, nil
	}
	return codexTransportManaged, nil
}

func (v *CodexVoice) Request(ctx context.Context, s *surface.Session, method string, params map[string]any) error {
	if link := v.link(s.ID); link != nil {
		if method == "thread/realtime/start" {
			if err := link.realtimeLoginRefusal(); err != nil {
				return err
			}
		}
		_, err := link.client.Request(ctx, method, params, 15*time.Second)
		var rpc *codexRPCError
		if method == "thread/realtime/start" && errors.As(err, &rpc) {
			return link.startRefusal(err)
		}
		return err
	}
	_, err := v.Codex.requestSession(ctx, s, true, method, params, 15*time.Second)
	var rpc *codexRPCError
	if method == "thread/realtime/start" && errors.As(err, &rpc) {
		return &voice.RejectedError{Reason: rpc.Message}
	}
	return err
}

func (v *CodexVoice) Release(threadID string, cursor voice.Cursor) {
	if link := v.link(threadID); link != nil && link.source == cursor.Source {
		v.dropLink(threadID, link)
	}
}

func (v *CodexVoice) Interrupt(ctx context.Context, s *surface.Session) error {
	return v.Codex.Interrupt(ctx, s)
}

func (v *CodexVoice) Cursor(ctx context.Context, s *surface.Session) (voice.Cursor, error) {
	route, err := v.voiceRoute(ctx, s)
	if err != nil {
		return voice.Cursor{}, err
	}
	if previous := v.link(s.ID); previous != nil {
		v.dropLink(s.ID, previous)
	}
	setCodexTransport(s, route)
	if route == codexTransportManaged {
		link, err := v.openVoiceLink(ctx, s)
		if err != nil {
			return voice.Cursor{}, err
		}
		v.mu.Lock()
		if v.links == nil {
			v.links = map[string]*voiceLink{}
		}
		v.links[s.ID] = link
		v.mu.Unlock()
		return voice.Cursor{Source: link.source}, nil
	}
	return v.desktopCursor(ctx)
}

func (v *CodexVoice) desktopCursor(ctx context.Context) (voice.Cursor, error) {
	conn, err := v.Codex.dial(ctx)
	if err != nil {
		return voice.Cursor{}, err
	}
	defer conn.close()
	value, err := conn.evaluate(ctx, codexEventCursorJS, 3*time.Second)
	position, ok := value.(float64)
	if err != nil {
		return voice.Cursor{}, err
	}
	if !ok {
		return voice.Cursor{}, fmt.Errorf("Codex event cursor is unavailable")
	}
	return voice.Cursor{Source: conn.target, Position: int64(position)}, nil
}

func (v *CodexVoice) Poll(ctx context.Context, after voice.Cursor, threadID string, wait time.Duration) (voice.Batch, error) {
	if link := v.link(threadID); link != nil {
		batch, err := link.poll(ctx, after, wait)
		if err != nil {
			return batch, err
		}
		ended := batch.Lost && link.client.failed()
		for _, event := range batch.Events {
			ended = ended || event.Method == "thread/realtime/closed"
		}
		if ended {
			v.dropLink(threadID, link)
		}
		return batch, nil
	}
	if strings.HasPrefix(after.Source, "managed:") {
		return voice.Batch{Cursor: after, Lost: true}, nil
	}
	conn, err := v.Codex.dial(ctx)
	if err != nil {
		return voice.Batch{}, err
	}
	defer conn.close()
	stop := context.AfterFunc(ctx, func() { _ = conn.close() })
	defer stop()
	value, err := conn.evaluate(ctx, codexVoiceEventsJS(after.Position, threadID, wait), wait+3*time.Second)
	if err != nil {
		return voice.Batch{}, err
	}
	var wire struct {
		Cursor int64         `json:"cursor"`
		Events []voice.Event `json:"events"`
		Lost   bool          `json:"lost"`
	}
	text, ok := value.(string)
	if !ok {
		return voice.Batch{}, fmt.Errorf("Codex returned no voice events")
	}
	if err = json.Unmarshal([]byte(text), &wire); err != nil {
		return voice.Batch{}, err
	}
	return voice.Batch{Cursor: voice.Cursor{Source: conn.target, Position: wire.Cursor}, Events: wire.Events, Lost: wire.Lost || conn.target != after.Source}, nil
}

func (v *CodexVoice) RespondDynamicToolCall(ctx context.Context, requestID, threadID string, success bool, text string) error {
	if link := v.link(threadID); link != nil {
		return link.respond(requestID, threadID, success, text)
	}
	conn, err := v.Codex.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	value, err := conn.evaluate(ctx, codexDynamicToolResponseJS(requestID, threadID, success, text), 3*time.Second)
	if err != nil {
		return err
	}
	if value != "ok" {
		return fmt.Errorf("Codex did not accept dynamic tool response: %v", value)
	}
	return nil
}

func codexVoiceEventsJS(after int64, threadID string, wait time.Duration) string {
	return fmt.Sprintf(`(async()=>{const b=globalThis.__agenthailCodexDesktopRendererV2;if(!b||typeof b.voiceEvents!=='function')throw Error('Codex event bridge unavailable');return JSON.stringify(await b.voiceEvents(%d,%s,%d))})()`, after, strconvQuote(threadID), wait.Milliseconds())
}
