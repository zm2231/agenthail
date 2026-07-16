package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const maxCodexTranscriptRecordBytes = 32 * 1024 * 1024

type codexContextState struct {
	offset       int64
	usage        surface.ContextUsage
	awaitingPost bool
	pendingPre   int64
	liveResolved bool
}

type codexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type codexContextRecord struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type         string `json:"type"`
		WindowNumber int    `json:"window_number"`
		WindowID     string `json:"window_id"`
		Info         struct {
			TotalTokenUsage    codexTokenUsage `json:"total_token_usage"`
			LastTokenUsage     codexTokenUsage `json:"last_token_usage"`
			ModelContextWindow int64           `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

func (c *Codex) ContextUsage(ctx context.Context, sess *surface.Session) (*surface.ContextUsage, error) {
	path := codexTranscriptPath(sess)
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	c.contextMu.Lock()
	defer c.contextMu.Unlock()
	if c.contextState == nil {
		c.contextState = map[string]*codexContextState{}
	}
	state := c.contextState[path]
	if state == nil || info.Size() < state.offset {
		state = &codexContextState{}
		c.contextState[path] = state
	}
	offset, err := scanAppendedJSONL(ctx, path, state.offset, maxCodexTranscriptRecordBytes, func(line []byte) error {
		var record codexContextRecord
		if json.Unmarshal(line, &record) != nil {
			return nil
		}
		at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
		switch {
		case record.Type == "event_msg" && record.Payload.Type == "token_count" && record.Payload.Info.LastTokenUsage.TotalTokens > 0:
			last := record.Payload.Info.LastTokenUsage
			total := record.Payload.Info.TotalTokenUsage
			state.usage.UsedTokens = last.TotalTokens
			state.usage.ContextWindow = record.Payload.Info.ModelContextWindow
			state.usage.CumulativeTokens = total.TotalTokens
			state.usage.InputTokens = last.InputTokens
			state.usage.CachedInputTokens = last.CachedInputTokens
			state.usage.OutputTokens = last.OutputTokens
			state.usage.ReasoningOutputTokens = last.ReasoningOutputTokens
			state.usage.UpdatedAt = at
			if state.awaitingPost && last.TotalTokens < state.pendingPre {
				state.usage.PreCompactTokens = state.pendingPre
				state.usage.PostCompactTokens = last.TotalTokens
				state.usage.ReclaimedTokens = state.pendingPre - last.TotalTokens
				state.awaitingPost = false
				state.pendingPre = 0
				state.liveResolved = false
				state.usage.Compacting = false
			}
		case record.Type == "compacted" && record.Payload.WindowNumber > state.usage.CompactionCount:
			state.usage.CompactionCount = record.Payload.WindowNumber
			compactedAt := uuidV7Time(record.Payload.WindowID)
			if compactedAt.IsZero() {
				compactedAt = at
			}
			state.usage.LastCompactedAt = compactedAt
			if state.usage.UsedTokens > 0 {
				state.pendingPre = state.usage.UsedTokens
				if state.liveResolved && state.usage.PreCompactTokens > 0 {
					state.pendingPre = state.usage.PreCompactTokens
				}
				state.liveResolved = false
				state.usage.PostCompactTokens = 0
				state.usage.ReclaimedTokens = 0
				state.awaitingPost = true
			}
		}
		return nil
	})
	state.offset = offset
	if err != nil {
		return nil, err
	}
	if state.usage.ContextWindow == 0 && state.usage.CompactionCount == 0 {
		return nil, nil
	}
	usage := state.usage
	return &usage, nil
}

func codexTranscriptPath(sess *surface.Session) string {
	if sess.Transcript != "" {
		return sess.Transcript
	}
	at := uuidV7Time(sess.ID)
	if at.IsZero() {
		return ""
	}
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".codex")
	}
	dates := []time.Time{at.In(time.Local), at}
	for _, base := range append([]time.Time(nil), dates...) {
		dates = append(dates, base.AddDate(0, 0, -1), base.AddDate(0, 0, 1))
	}
	seen := map[string]bool{}
	var matches []string
	for _, date := range dates {
		day := date.Format("2006/01/02")
		if seen[day] {
			continue
		}
		seen[day] = true
		pattern := filepath.Join(home, "sessions", date.Format("2006"), date.Format("01"), date.Format("02"), "*"+sess.ID+"*.jsonl")
		found, _ := filepath.Glob(pattern)
		matches = append(matches, found...)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func uuidV7Time(value string) time.Time {
	hex := strings.ReplaceAll(value, "-", "")
	if len(hex) < 13 || hex[12] != '7' {
		return time.Time{}
	}
	millis, err := strconv.ParseInt(hex[:12], 16, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(millis).UTC()
}

func codexContextEvent(event codexEvent, current surface.ContextUsage) (*surface.ContextUsage, bool) {
	lower := strings.ToLower(event.Method)
	if strings.Contains(lower, "tokenusage") {
		usage := findCodexMap(event.Params, "tokenUsage")
		if usage == nil {
			usage = event.Params
		}
		last, _ := usage["last"].(map[string]any)
		total, _ := usage["total"].(map[string]any)
		if codexNumber(last["totalTokens"]) == 0 {
			return nil, false
		}
		current.UsedTokens = codexNumber(last["totalTokens"])
		current.ContextWindow = codexNumber(usage["modelContextWindow"])
		current.CumulativeTokens = codexNumber(total["totalTokens"])
		current.InputTokens = codexNumber(last["inputTokens"])
		current.CachedInputTokens = codexNumber(last["cachedInputTokens"])
		current.OutputTokens = codexNumber(last["outputTokens"])
		current.ReasoningOutputTokens = codexNumber(last["reasoningOutputTokens"])
		current.UpdatedAt = time.Now().UTC()
		return &current, true
	}
	if !codexContextCompaction(event.Params) {
		return nil, false
	}
	switch {
	case strings.Contains(lower, "started"):
		current.Compacting = true
		return &current, true
	case strings.Contains(lower, "completed") || strings.Contains(lower, "compacted"):
		current.Compacting = false
		if current.LastCompactedAt.IsZero() {
			current.LastCompactedAt = time.Now().UTC()
		}
		return &current, true
	default:
		return nil, false
	}
}

func (c *Codex) applyContextEvent(sess *surface.Session, event codexEvent, current surface.ContextUsage) (*surface.ContextUsage, bool) {
	usage, ok := codexContextEvent(event, current)
	if !ok {
		return nil, false
	}
	path := codexTranscriptPath(sess)
	if path == "" {
		return usage, true
	}
	c.contextMu.Lock()
	defer c.contextMu.Unlock()
	if state := c.contextState[path]; state != nil {
		lower := strings.ToLower(event.Method)
		if codexContextCompaction(event.Params) && strings.Contains(lower, "started") {
			state.pendingPre = state.usage.UsedTokens
			state.awaitingPost = state.pendingPre > 0
			state.liveResolved = false
		}
		if strings.Contains(lower, "tokenusage") && state.awaitingPost {
			if usage.UsedTokens < state.pendingPre {
				usage.PreCompactTokens = state.pendingPre
				usage.PostCompactTokens = usage.UsedTokens
				usage.ReclaimedTokens = state.pendingPre - usage.UsedTokens
				state.awaitingPost = false
				state.pendingPre = 0
				state.liveResolved = true
			} else {
				usage.PreCompactTokens = state.usage.PreCompactTokens
				usage.PostCompactTokens = state.usage.PostCompactTokens
				usage.ReclaimedTokens = state.usage.ReclaimedTokens
			}
		}
		state.usage = *usage
	}
	return usage, true
}

func findCodexMap(value any, key string) map[string]any {
	current, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if result, ok := current[key].(map[string]any); ok {
		return result
	}
	for _, child := range current {
		if result := findCodexMap(child, key); result != nil {
			return result
		}
	}
	return nil
}

func codexContextCompaction(value any) bool {
	switch current := value.(type) {
	case string:
		return strings.EqualFold(current, "contextCompaction")
	case map[string]any:
		for key, child := range current {
			if key == "type" && codexContextCompaction(child) {
				return true
			}
			if codexContextCompaction(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if codexContextCompaction(child) {
				return true
			}
		}
	}
	return false
}

func codexNumber(value any) int64 {
	switch number := value.(type) {
	case float64:
		return int64(number)
	case int64:
		return number
	case int:
		return int64(number)
	case json.Number:
		result, _ := number.Int64()
		return result
	default:
		return 0
	}
}
