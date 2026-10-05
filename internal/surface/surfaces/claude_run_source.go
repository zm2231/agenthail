package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// ClaudeRunObservation is the provider-owned metadata available in a local
// Claude Code background job record. Fields absent from that record remain
// absent; this source does not infer lifecycle state from transcript text.
type ClaudeRunObservation = surface.ClaudeRunObservation
type ClaudeSubagentLink = surface.ClaudeSubagentLink

// ClaudeContextWindowObservation is the only context-window claim a Claude
// provider may make for a session. A zero window is deliberate: it means the
// provider has no authoritative capacity for that session and callers must
// render usage as tokens, not a percentage.
type ClaudeContextWindowObservation struct {
	Window   int64  `json:"window,omitempty"`
	Source   string `json:"source"`
	Model    string `json:"model,omitempty"`
	Reliable bool   `json:"reliable"`
}

const (
	ClaudeContextWindowSourceConfigured = "configured"
	ClaudeContextWindowSourceProvider   = "provider"
	ClaudeContextWindowSourceUnknown    = "unknown"
)

// ObserveClaudeConfiguredContextWindow validates an explicit launch/config
// value. Transcript model names must not be passed here: they do not preserve
// the per-session context-window selection reliably.
func ObserveClaudeConfiguredContextWindow(model string) ClaudeContextWindowObservation {
	model = strings.TrimSpace(model)
	if model == "" {
		return ClaudeContextWindowObservation{Source: ClaudeContextWindowSourceUnknown}
	}
	if strings.Contains(strings.ToLower(model), "[1m]") {
		return ClaudeContextWindowObservation{Window: 1_000_000, Source: ClaudeContextWindowSourceConfigured, Model: model, Reliable: true}
	}
	return ClaudeContextWindowObservation{Source: ClaudeContextWindowSourceUnknown, Model: model}
}

type claudeRunRecord struct {
	Template        string `json:"template"`
	State           string `json:"state"`
	SessionID       string `json:"sessionId"`
	ResumeSessionID string `json:"resumeSessionId"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type claudeSubagentRecord struct {
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId"`
}

// ObserveClaudeRuns reads the installed Claude Code job-record producer
// without invoking Claude Code or changing any session. It observes only
// background records under <home>/.claude/jobs.
func ObserveClaudeRuns(ctx context.Context, home string) ([]ClaudeRunObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home: %w", err)
		}
	}
	paths, err := filepath.Glob(filepath.Join(home, ".claude", "jobs", "*", "state.json"))
	if err != nil {
		return nil, fmt.Errorf("discover Claude job records: %w", err)
	}
	sort.Strings(paths)
	observations := make([]ClaudeRunObservation, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		observation, err := readClaudeRunRecord(path)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

func readClaudeRunRecord(path string) (ClaudeRunObservation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClaudeRunObservation{}, fmt.Errorf("read Claude job record %s: %w", path, err)
	}
	var record claudeRunRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return ClaudeRunObservation{}, fmt.Errorf("parse Claude job record %s: %w", path, err)
	}
	createdAt, err := parseClaudeRunTime(record.CreatedAt, "createdAt", path)
	if err != nil {
		return ClaudeRunObservation{}, err
	}
	updatedAt, err := parseClaudeRunTime(record.UpdatedAt, "updatedAt", path)
	if err != nil {
		return ClaudeRunObservation{}, err
	}
	jobID := filepath.Base(filepath.Dir(path))
	return ClaudeRunObservation{
		RecordPath:      path,
		JobID:           jobID,
		SessionID:       record.SessionID,
		ResumeSessionID: record.ResumeSessionID,
		RunType:         record.Template,
		ProviderState:   record.State,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}, nil
}

func parseClaudeRunTime(value, field, path string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse Claude job record %s %s: %w", path, field, err)
	}
	return at, nil
}

// ObserveClaudeSubagentLinks reads the installed Claude Code subagent
// transcript topology without invoking Claude Code or changing any session.
// A non-empty parentSessionID reads only that session's subagent directory;
// an empty one reads every session.
func ObserveClaudeSubagentLinks(ctx context.Context, home, parentSessionID string) ([]ClaudeSubagentLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home: %w", err)
		}
	}
	parent := "*"
	if parentSessionID != "" {
		if parentSessionID == "." || parentSessionID == ".." || strings.ContainsAny(parentSessionID, `*?[]\/`) {
			return nil, fmt.Errorf("invalid Claude session ID %q", parentSessionID)
		}
		parent = parentSessionID
	}
	paths, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", parent, "subagents", "agent-*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("discover Claude subagent transcripts: %w", err)
	}
	sort.Strings(paths)
	links := make([]ClaudeSubagentLink, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		link, err := readClaudeSubagentLink(path)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, nil
}

func readClaudeSubagentLink(path string) (ClaudeSubagentLink, error) {
	parentSessionID := filepath.Base(filepath.Dir(filepath.Dir(path)))
	filename := filepath.Base(path)
	agentID := filename[len("agent-") : len(filename)-len(".jsonl")]
	if parentSessionID == "" || agentID == "" {
		return ClaudeSubagentLink{}, fmt.Errorf("invalid Claude subagent transcript path %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return ClaudeSubagentLink{}, fmt.Errorf("open Claude subagent transcript %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxClaudeTranscriptRecordBytes)
	for scanner.Scan() {
		var record claudeSubagentRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.SessionID == "" || record.AgentID == "" {
			continue
		}
		if record.SessionID != parentSessionID || record.AgentID != agentID {
			return ClaudeSubagentLink{}, fmt.Errorf("Claude subagent transcript %s does not match its path", path)
		}
		return ClaudeSubagentLink{ParentSessionID: parentSessionID, AgentID: agentID, TranscriptPath: path}, nil
	}
	if err := scanner.Err(); err != nil {
		return ClaudeSubagentLink{}, fmt.Errorf("read Claude subagent transcript %s: %w", path, err)
	}
	return ClaudeSubagentLink{}, fmt.Errorf("Claude subagent transcript %s has no identifying record", path)
}
