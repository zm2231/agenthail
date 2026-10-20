package surfaces

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// codexTurnMeter accumulates, one rollout record at a time, the model and
// token usage of the turn in progress. Usage is known only for a turn whose
// start record it saw, since a turn joined midway would undercount.
type codexTurnMeter struct {
	started   bool
	turnID    string
	model     string
	usage     surface.TurnUsage
	lastTotal int64
	// context is the latest request's size, which outlives the turn so a
	// compaction at the start of the next one knows what it compacted.
	context          *int64
	compactions      []surface.TurnCompaction
	compactionsKnown bool
	awaitPost        bool
	// between is set once the meter saw a turn end or read from the start,
	// so the compactions before the next start are all known and its own.
	between bool
}

func newCodexTurnMeter() *codexTurnMeter {
	return &codexTurnMeter{lastTotal: -1}
}

func codexRecordStartsTurn(record map[string]any) bool {
	payload, _ := record["payload"].(map[string]any)
	typ := str(payload, "type")
	return str(record, "type") == "event_msg" && (typ == "task_started" || typ == "turn_started")
}

func codexRecordEndsTurn(record map[string]any) (string, bool) {
	payload, _ := record["payload"].(map[string]any)
	switch typ := str(payload, "type"); typ {
	case "task_complete", "task_completed", "task_cancelled", "task_canceled", "task_aborted", "turn_completed", "turn_aborted", "interrupted":
		return typ, true
	}
	return "", false
}

func (m *codexTurnMeter) observe(record map[string]any) *surface.TurnMetadata {
	payload, _ := record["payload"].(map[string]any)
	if codexRecordStartsTurn(record) {
		*m = codexTurnMeter{started: true, turnID: codexRecordTurnID(record), lastTotal: m.lastTotal, context: m.context, compactions: m.compactions, compactionsKnown: m.between, awaitPost: m.awaitPost}
		return nil
	}
	if str(record, "type") == "compacted" {
		m.compactions = append(m.compactions, surface.TurnCompaction{Trigger: surface.CompactionUnknown, PreTokens: m.context})
		m.awaitPost = true
		return nil
	}
	if str(record, "type") == "turn_context" {
		if turnID := str(payload, "turn_id"); m.turnID == "" || turnID == "" || turnID == m.turnID {
			m.model = str(payload, "model")
		}
		return nil
	}
	if str(record, "type") == "event_msg" && str(payload, "type") == "token_count" {
		m.count(payload)
		return nil
	}
	status, ended := codexRecordEndsTurn(record)
	if !ended {
		return nil
	}
	var duration *int64
	if value, ok := payload["duration_ms"]; ok {
		ms := int64Value(value)
		duration = &ms
	}
	measure := surface.TurnMeasure{DurationMs: duration}
	if m.started {
		measured := m.usage
		measure.Model, measure.Usage, measure.ContextTokens = m.model, &measured, m.context
		if m.compactionsKnown {
			measure.Compactions = append([]surface.TurnCompaction{}, m.compactions...)
		}
	}
	*m = codexTurnMeter{lastTotal: m.lastTotal, context: m.context, awaitPost: m.awaitPost, between: true}
	return surface.NewTurnMetadata(surface.TurnStateOfDone(status), measure)
}

// count adds one model request. Codex repeats a token_count record without a
// new request, which leaves the running total unchanged.
func (m *codexTurnMeter) count(payload map[string]any) {
	info, _ := payload["info"].(map[string]any)
	total, _ := info["total_token_usage"].(map[string]any)
	last, _ := info["last_token_usage"].(map[string]any)
	if last == nil {
		return
	}
	if size := int64Value(last["total_tokens"]); size > 0 {
		m.context = &size
		if n := len(m.compactions); m.awaitPost && n > 0 && (m.compactions[n-1].PreTokens == nil || size < *m.compactions[n-1].PreTokens) {
			m.compactions[n-1].PostTokens = &size
			m.awaitPost = false
		}
	}
	if total != nil {
		running := int64Value(total["total_tokens"])
		if running == m.lastTotal {
			return
		}
		m.lastTotal = running
	}
	m.usage.InputTokens += int64Value(last["input_tokens"])
	m.usage.CachedInputTokens += int64Value(last["cached_input_tokens"])
	m.usage.CacheWriteTokens += int64Value(last["cache_write_input_tokens"])
	m.usage.OutputTokens += int64Value(last["output_tokens"])
	if value, ok := last["reasoning_output_tokens"]; ok {
		if m.usage.ReasoningTokens == nil {
			m.usage.ReasoningTokens = new(int64)
		}
		*m.usage.ReasoningTokens += int64Value(value)
	}
}

func codexTurnMeterAt(ctx context.Context, file *os.File, offset int64) (*codexTurnMeter, error) {
	meter := newCodexTurnMeter()
	from, found, err := turnReplayStart(ctx, file, offset, []byte(`_started"`), codexRecordStartsTurn)
	if err != nil || !found {
		return meter, err
	}
	meter.between = from == 0
	err = scanTranscriptRecords(ctx, file, from, offset, func(record map[string]any, _ int64, _ []byte) {
		meter.observe(record)
	})
	return meter, err
}

func codexStreamTurnMeter(ctx context.Context, path string, offset int64) (*codexTurnMeter, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Codex local transcript is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	defer file.Close()
	return codexTurnMeterAt(ctx, file, offset)
}

func codexRPCTurnMetadata(turn codexTurn) *surface.TurnMetadata {
	state := surface.TurnUnknown
	switch {
	case turn.Status == surface.StatusBusy:
		state = surface.TurnRunning
	case turn.Done:
		state = surface.TurnStateOfDone(strings.TrimPrefix(turn.Error, "turn "))
	}
	return surface.NewTurnMetadata(state, surface.TurnMeasure{})
}

// codexRolloutTurnMetadata reads what the rollout recorded about a finished
// turn, or nil when the rollout has not recorded the turn's end.
func codexRolloutTurnMetadata(ctx context.Context, path, turnID string) *surface.TurnMetadata {
	if path == "" || turnID == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	end, found, err := transcriptRecordBefore(ctx, file, info.Size(), []byte(turnID), func(record map[string]any) bool {
		_, ended := codexRecordEndsTurn(record)
		return ended && codexRecordTurnID(record) == turnID
	})
	if err != nil || !found {
		return nil
	}
	meter, err := codexTurnMeterAt(ctx, file, end)
	if err != nil {
		return nil
	}
	var turn *surface.TurnMetadata
	_ = scanTranscriptRecords(ctx, file, end, info.Size(), func(record map[string]any, _ int64, _ []byte) {
		if turn == nil {
			turn = meter.observe(record)
		}
	})
	return turn
}

// codexRecordedTurns maps each turn a transcript page saw end to what its
// rollout recorded, since app-server turns carry no usage.
func codexRecordedTurns(items []surface.TimelineItem) map[string]*surface.TurnMetadata {
	recorded := map[string]*surface.TurnMetadata{}
	for _, item := range items {
		if item.Kind == "done" && item.TurnID != "" && item.Turn != nil {
			recorded[item.TurnID] = item.Turn
		}
	}
	return recorded
}
