package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

type CodexVoice struct{ Codex *Codex }

func (v *CodexVoice) Create(ctx context.Context, cwd, instructions string) (*surface.Session, error) {
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
		"threadSource": "agenthail", "serviceName": "agenthail-voice", "dynamicTools": voiceDynamicTools(),
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
	return &surface.Session{ID: id, Name: "Agenthail voice orchestrator", Cwd: cwd,
		Surface: surface.KindCodex, Source: "agenthail", Transport: codexTransportDesktop,
		HasLocal: true, Status: surface.StatusIdle, Transcript: str(thread, "path"), LastActive: time.Now()}, nil
}

func voiceDynamicTools() []map[string]any {
	return []map[string]any{{
		"type": "namespace", "name": "agenthail", "description": "Agenthail operations available to the persistent voice orchestrator.",
		"tools": []map[string]any{
			{"type": "function", "name": "voice_transfer", "description": "Transfer the connected voice call to one exact existing Codex or Claude session.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"targetId": map[string]any{"type": "string", "description": "Exact Agenthail session ID or alias."}}, "required": []string{"targetId"}, "additionalProperties": false}},
			{"type": "function", "name": "voice_return_to_orchestrator", "description": "Return the connected voice call to Agenthail's normal orchestration workflow.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
		},
	}}
}

func (v *CodexVoice) Request(ctx context.Context, s *surface.Session, method string, params map[string]any) error {
	_, err := v.Codex.requestSession(ctx, s, true, method, params, 15*time.Second)
	return err
}

func (v *CodexVoice) Interrupt(ctx context.Context, s *surface.Session) error {
	return v.Codex.Interrupt(ctx, s)
}

func (v *CodexVoice) Cursor(ctx context.Context) (voice.Cursor, error) {
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

func (v *CodexVoice) Poll(ctx context.Context, after voice.Cursor, threadID string) (voice.Batch, error) {
	conn, err := v.Codex.dial(ctx)
	if err != nil {
		return voice.Batch{}, err
	}
	defer conn.close()
	value, err := conn.evaluate(ctx, codexVoiceEventsJS(after.Position, threadID), 3*time.Second)
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

func (v *CodexVoice) RespondDynamicToolCall(ctx context.Context, requestID string, success bool, text string) error {
	conn, err := v.Codex.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	value, err := conn.evaluate(ctx, codexDynamicToolResponseJS(requestID, success, text), 3*time.Second)
	if err != nil {
		return err
	}
	if value != "ok" {
		return fmt.Errorf("Codex did not accept dynamic tool response: %v", value)
	}
	return nil
}

func codexVoiceEventsJS(after int64, threadID string) string {
	return fmt.Sprintf(`(()=>{const b=globalThis.__agenthailCodexDesktopRendererV2;if(!b)throw Error('Codex event bridge unavailable');const after=%d;return JSON.stringify({cursor:b.sequence,lost:after>b.sequence||(b.events.length>0&&b.events[0].sequence>after+1),events:b.events.filter(e=>e.sequence>after&&e.params.threadId===%s&&((e.method.startsWith('thread/realtime/')&&e.method!=='thread/realtime/outputAudio/delta')||e.method==='item/tool/call'||e.method==='turn/started'||(e.method==='item/completed'&&e.params.item?.type==='agentMessage'&&e.params.item?.phase==='final_answer')))})})()`, after, strconvQuote(threadID))
}
