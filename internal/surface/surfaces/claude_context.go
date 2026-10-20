package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeContextState struct {
	offset              int64
	fileInfo            os.FileInfo
	usage               surface.ContextUsage
	latestCommandAt     time.Time
	latestCommandDoneAt time.Time
	seenBoundaries      map[string]bool
	request             claudeRequestWindow
}

// claudeRequestWindow is what the newest API request Claude logged says about
// the window: a request carrying the 1M context beta runs with a 1M window.
type claudeRequestWindow struct {
	seen  bool
	model string
	oneM  bool
}

type claudeContextRecord struct {
	UUID           string `json:"uuid"`
	Type           string `json:"type"`
	Subtype        string `json:"subtype"`
	Timestamp      string `json:"timestamp"`
	RequestedModel string `json:"requestedModel"`
	Params         struct {
		Model string   `json:"model"`
		Betas []string `json:"betas"`
	} `json:"params"`
	CompactMetadata struct {
		PreTokens  int64 `json:"preTokens"`
		PostTokens int64 `json:"postTokens"`
	} `json:"compactMetadata"`
	Message struct {
		Model   string `json:"model"`
		Content any    `json:"content"`
		Usage   struct {
			InputTokens         int64 `json:"input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func (c *Claude) ContextUsage(ctx context.Context, sess *surface.Session) (*surface.ContextUsage, error) {
	path, err := c.sessionTranscript(sess)
	if err != nil || path == "" {
		return nil, err
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
		c.contextState = map[string]*claudeContextState{}
	}
	state := c.contextState[path]
	if state == nil || info.Size() < state.offset || (state.fileInfo != nil && !os.SameFile(state.fileInfo, info)) {
		state = &claudeContextState{seenBoundaries: map[string]bool{}}
		c.contextState[path] = state
	}
	if state.seenBoundaries == nil {
		state.seenBoundaries = map[string]bool{}
	}
	visit := func(line []byte) error {
		var record claudeContextRecord
		if json.Unmarshal(line, &record) != nil {
			return nil
		}
		at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
		switch {
		case record.Type == "api-request":
			state.request = claudeRequestWindow{seen: true, model: record.Params.Model}
			for _, beta := range record.Params.Betas {
				if strings.HasPrefix(beta, "context-1m-") {
					state.request.oneM = true
				}
			}
		case record.Type == "assistant":
			used := record.Message.Usage.InputTokens + record.Message.Usage.CacheCreationTokens + record.Message.Usage.CacheReadTokens
			if used > 0 && contextEventIsCurrent(at, state.usage.UpdatedAt) {
				state.usage.UsedTokens = used
				state.usage.InputTokens = record.Message.Usage.InputTokens
				state.usage.CachedInputTokens = record.Message.Usage.CacheCreationTokens + record.Message.Usage.CacheReadTokens
				state.usage.OutputTokens = record.Message.Usage.OutputTokens
				window := observeClaudeCurrentContextWindow(sess.ConfiguredModel, state.request, record.RequestedModel, record.Message.Model)
				state.usage.ContextWindow = window.Window
				state.usage.ContextWindowSource = window.Source
				state.usage.WindowEstimated = false
				state.usage.UpdatedAt = at
			}
		case record.Type == "user":
			content := strings.TrimSpace(transcriptText(record.Message.Content))
			switch {
			case content == "/compact" && compactCommandIsNewer(at, state.usage.LastCompactedAt):
				state.rememberCommandTime(at)
			case strings.Contains(content, "<command-name>/compact</command-name>") && compactCommandIsNewer(at, state.usage.LastCompactedAt):
				state.rememberCommandTime(at)
			case strings.HasPrefix(content, "<local-command-stdout>"):
				state.rememberCommandCompletion(at)
			}
		case record.Type == "system" && record.Subtype == "compact_boundary":
			key := compactBoundaryKey(record)
			if state.seenBoundaries[key] {
				return nil
			}
			state.seenBoundaries[key] = true
			state.usage.CompactionCount++
			if contextEventIsCurrent(at, state.usage.LastCompactedAt) {
				state.usage.PreCompactTokens = record.CompactMetadata.PreTokens
				state.usage.PostCompactTokens = record.CompactMetadata.PostTokens
				state.usage.ReclaimedTokens = 0
				if record.CompactMetadata.PreTokens > record.CompactMetadata.PostTokens {
					state.usage.ReclaimedTokens = record.CompactMetadata.PreTokens - record.CompactMetadata.PostTokens
				}
				state.usage.LastCompactedAt = at
				if contextEventIsCurrent(at, state.usage.UpdatedAt) {
					state.usage.UsedTokens = record.CompactMetadata.PostTokens
					state.usage.InputTokens = 0
					state.usage.CachedInputTokens = 0
					state.usage.OutputTokens = 0
					state.usage.UpdatedAt = at
				}
			}
		case record.Type == "system" && record.Subtype == "local_command":
			state.rememberCommandCompletion(at)
		}
		return nil
	}
	if state.offset == 0 {
		lines, offset, readErr := readRecentJSONLLines(ctx, path, 0, initialClaudeObservationBytes, maxClaudeTranscriptRecordBytes)
		if readErr != nil {
			return nil, readErr
		}
		for _, line := range lines {
			if err := visit(line); err != nil {
				return nil, err
			}
		}
		state.offset = offset
		state.fileInfo = info
	} else {
		offset, scanErr := scanAppendedJSONL(ctx, path, state.offset, maxClaudeTranscriptRecordBytes, visit)
		state.offset = offset
		state.fileInfo = info
		if scanErr != nil {
			return nil, scanErr
		}
	}
	completedAt := state.usage.LastCompactedAt
	if state.latestCommandDoneAt.After(completedAt) {
		completedAt = state.latestCommandDoneAt
	}
	state.usage.Compacting = !state.latestCommandAt.IsZero() && state.latestCommandAt.After(completedAt)
	if state.usage.UsedTokens == 0 && state.usage.ContextWindow == 0 && state.usage.CompactionCount == 0 {
		return nil, nil
	}
	usage := state.usage
	return &usage, nil
}

// observeClaudeCurrentContextWindow prefers the newest API request for the
// replying model, then the model the record says was requested, then the
// launch model. message.model never carries the [1m] selection, so it only
// rules out a window chosen for a different model.
func observeClaudeCurrentContextWindow(configuredModel string, request claudeRequestWindow, requestedModel, observedModel string) ClaudeContextWindowObservation {
	observedBase := claudeModelBase(observedModel)
	if request.seen && (observedBase == "" || claudeModelBase(request.model) == observedBase) {
		if request.oneM {
			return ClaudeContextWindowObservation{Window: 1_000_000, Source: ClaudeContextWindowSourceRequest, Model: request.model, Reliable: true}
		}
		return ClaudeContextWindowObservation{Source: ClaudeContextWindowSourceUnknown, Model: request.model}
	}
	window := ObserveClaudeConfiguredContextWindow(configuredModel)
	selected := configuredModel
	if strings.TrimSpace(requestedModel) != "" {
		window, selected = ObserveClaudeConfiguredContextWindow(requestedModel), requestedModel
		if window.Window > 0 {
			window.Source = ClaudeContextWindowSourceTranscript
		}
	}
	selectedBase := claudeModelBase(selected)
	if window.Window > 0 && selectedBase != "" && observedBase != "" && selectedBase != observedBase {
		return ClaudeContextWindowObservation{Source: ClaudeContextWindowSourceUnknown, Model: observedModel}
	}
	return window
}

func claudeModelBase(model string) string {
	model = strings.TrimSpace(model)
	if index := strings.IndexByte(model, '['); index >= 0 {
		model = model[:index]
	}
	return strings.TrimSpace(model)
}

func compactBoundaryKey(record claudeContextRecord) string {
	if record.UUID != "" {
		return record.UUID
	}
	return record.Timestamp + ":" + strconv.FormatInt(record.CompactMetadata.PreTokens, 10) + ":" + strconv.FormatInt(record.CompactMetadata.PostTokens, 10)
}

func contextEventIsCurrent(candidate, current time.Time) bool {
	return current.IsZero() || (!candidate.IsZero() && !candidate.Before(current))
}

func (s *claudeContextState) rememberCommandTime(at time.Time) {
	if contextEventIsCurrent(at, s.latestCommandAt) {
		s.latestCommandAt = at
	}
}

func (s *claudeContextState) rememberCommandCompletion(at time.Time) {
	if contextEventIsCurrent(at, s.latestCommandDoneAt) {
		s.latestCommandDoneAt = at
	}
}

func compactCommandIsNewer(commandAt, boundaryAt time.Time) bool {
	return boundaryAt.IsZero() || (!commandAt.IsZero() && commandAt.After(boundaryAt))
}
