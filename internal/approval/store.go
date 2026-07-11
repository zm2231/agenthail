package approval

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	DefaultTTL       = 10 * time.Minute
	DefaultWait      = 2 * time.Minute
	MaxInputBytes    = 16 * 1024
	MaxSummaryLength = 512
	MaxListLimit     = 200
)

type Decision string

const (
	DecisionPending Decision = "pending"
	DecisionAllow   Decision = "allow"
	DecisionDeny    Decision = "deny"
)

type Request struct {
	ID        string          `json:"id"`
	Source    string          `json:"source"`
	SessionID string          `json:"sessionId,omitempty"`
	Tool      string          `json:"tool"`
	Action    string          `json:"action"`
	Summary   string          `json:"summary"`
	Input     json.RawMessage `json:"input,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	ExpiresAt time.Time       `json:"expiresAt"`
}

type Response struct {
	RequestID string   `json:"requestId"`
	Decision  Decision `json:"decision"`
	Reason    string   `json:"reason"`
}

type Record struct {
	Request
	Decision       Decision   `json:"decision"`
	DecisionReason string     `json:"decisionReason,omitempty"`
	DecidedAt      *time.Time `json:"decidedAt,omitempty"`
}

type Store struct {
	db *sql.DB
}

func DefaultPath() string {
	if configured := os.Getenv("AGENTHAIL_APPROVAL_DB"); configured != "" {
		return configured
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "approvals.db")
}

func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create approval directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("create approval store: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close approval store: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, fmt.Errorf("protect approval store: %w", err)
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	db, err := sql.Open("sqlite", path+separator+"_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize approval store: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

const schema = `
CREATE TABLE IF NOT EXISTS approval_requests (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL,
	session_id TEXT NOT NULL DEFAULT '',
	tool TEXT NOT NULL,
	action TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	input_json TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	decision TEXT NOT NULL DEFAULT 'pending',
	decision_reason TEXT NOT NULL DEFAULT '',
	decided_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS approval_requests_created_at ON approval_requests(created_at DESC);
`

func Normalize(data []byte, now time.Time) (Request, error) {
	if len(data) == 0 {
		return Request{}, fmt.Errorf("approval request is empty")
	}
	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(data), 128<<10))
	if err := decoder.Decode(&payload); err != nil {
		return Request{}, fmt.Errorf("invalid approval request JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Request{}, fmt.Errorf("approval request must contain one JSON object")
		}
		return Request{}, fmt.Errorf("invalid approval request JSON: %w", err)
	}
	request := Request{
		Source:    stringField(payload, "source", "provider", "hook_event_name"),
		SessionID: stringField(payload, "session_id", "sessionId"),
		Tool:      stringField(payload, "tool_name", "tool", "command"),
		Action:    stringField(payload, "action", "permission_mode", "hook_event_name"),
		Summary:   stringField(payload, "summary", "reason", "description"),
		CreatedAt: now.UTC(),
		ExpiresAt: now.UTC().Add(DefaultTTL),
	}
	if request.Source == "" {
		request.Source = "local-hook"
	}
	if request.Tool == "" {
		return Request{}, fmt.Errorf("approval request tool is required")
	}
	if request.Action == "" {
		request.Action = request.Tool
	}
	if request.Summary == "" {
		request.Summary = request.Tool + " approval request"
	}
	request.Summary = boundText(request.Summary, MaxSummaryLength)
	request.Input = firstRaw(payload, "tool_input", "input")
	if len(request.Input) > 0 {
		canonical, err := canonicalJSON(request.Input)
		if err != nil {
			return Request{}, fmt.Errorf("approval request input is invalid JSON: %w", err)
		}
		request.Input = canonical
	}
	if len(request.Input) > MaxInputBytes {
		return Request{}, fmt.Errorf("approval request input exceeds %d bytes", MaxInputBytes)
	}
	if len(request.Input) == 0 {
		request.Input = json.RawMessage(`{}`)
	}
	request.ID = stringField(payload, "request_id", "requestId", "id")
	if request.ID == "" {
		hash := sha256.New()
		_, _ = hash.Write([]byte(request.Source + "\x00" + request.SessionID + "\x00" + request.Tool + "\x00" + request.Action + "\x00"))
		_, _ = hash.Write(request.Input)
		request.ID = "apr_" + hex.EncodeToString(hash.Sum(nil))[:24]
	}
	if err := validateID(request.ID); err != nil {
		return Request{}, err
	}
	return request, nil
}

func (s *Store) Observe(request Request, now time.Time) (Response, bool, error) {
	if s == nil || s.db == nil {
		return Response{}, false, fmt.Errorf("approval store is not open")
	}
	if err := validateRequest(request); err != nil {
		return Response{}, false, err
	}
	inserted := false
	result, err := s.db.Exec(`INSERT OR IGNORE INTO approval_requests(id,source,session_id,tool,action,summary,input_json,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`, request.ID, request.Source, request.SessionID, request.Tool, request.Action, request.Summary, string(request.Input), request.CreatedAt.UnixMilli(), request.ExpiresAt.UnixMilli())
	if err != nil {
		return Response{}, false, err
	}
	if count, _ := result.RowsAffected(); count == 1 {
		inserted = true
	}
	var decision Decision
	var reason string
	var expiresAt int64
	if err := s.db.QueryRow(`SELECT decision,decision_reason,expires_at FROM approval_requests WHERE id=?`, request.ID).Scan(&decision, &reason, &expiresAt); err != nil {
		return Response{}, false, err
	}
	if expiresAt <= now.UnixMilli() {
		if _, err := s.db.Exec(`UPDATE approval_requests SET source=?,session_id=?,tool=?,action=?,summary=?,input_json=?,created_at=?,expires_at=?,decision=?,decision_reason='',decided_at=0 WHERE id=?`, request.Source, request.SessionID, request.Tool, request.Action, request.Summary, string(request.Input), request.CreatedAt.UnixMilli(), request.ExpiresAt.UnixMilli(), DecisionPending, request.ID); err != nil {
			return Response{}, false, err
		}
		decision = DecisionPending
		inserted = true
	}
	if decision == DecisionPending {
		return Response{RequestID: request.ID, Decision: DecisionPending, Reason: "approval required; review with 'agenthail approval list'"}, inserted, nil
	}
	return Response{RequestID: request.ID, Decision: decision, Reason: reason}, false, nil
}

func (s *Store) Decide(id string, decision Decision, reason string, now time.Time) error {
	if err := validateID(id); err != nil {
		return err
	}
	if decision != DecisionAllow && decision != DecisionDeny {
		return fmt.Errorf("approval decision must be allow or deny")
	}
	result, err := s.db.Exec(`UPDATE approval_requests SET decision=?,decision_reason=?,decided_at=? WHERE id=? AND decision=? AND expires_at>?`, decision, boundText(reason, MaxSummaryLength), now.UnixMilli(), id, DecisionPending, now.UnixMilli())
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("approval request %q is missing, decided, or expired", id)
	}
	return nil
}

func (s *Store) List(limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	rows, err := s.db.Query(`SELECT id,source,session_id,tool,action,summary,input_json,created_at,expires_at,decision,decision_reason,decided_at FROM approval_requests ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Record, 0)
	for rows.Next() {
		var row Record
		var input string
		var createdAt, expiresAt, decidedAt int64
		if err := rows.Scan(&row.ID, &row.Source, &row.SessionID, &row.Tool, &row.Action, &row.Summary, &input, &createdAt, &expiresAt, &row.Decision, &row.DecisionReason, &decidedAt); err != nil {
			return nil, err
		}
		row.Input = json.RawMessage(input)
		row.CreatedAt = time.UnixMilli(createdAt).UTC()
		row.ExpiresAt = time.UnixMilli(expiresAt).UTC()
		if decidedAt > 0 {
			decided := time.UnixMilli(decidedAt).UTC()
			row.DecidedAt = &decided
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func validateRequest(request Request) error {
	if err := validateID(request.ID); err != nil {
		return err
	}
	if strings.TrimSpace(request.Tool) == "" {
		return fmt.Errorf("approval request tool is required")
	}
	if len(request.Input) > MaxInputBytes {
		return fmt.Errorf("approval request input exceeds %d bytes", MaxInputBytes)
	}
	return nil
}

func validateID(id string) error {
	if id == "" || len(id) > 128 || strings.ContainsAny(id, "\r\n\t ") {
		return fmt.Errorf("invalid approval request id")
	}
	return nil
}

func stringField(payload map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if raw, ok := payload[key]; ok {
			var value string
			if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func firstRaw(payload map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if raw, ok := payload[key]; ok {
			return append(json.RawMessage(nil), raw...)
		}
	}
	return nil
}

func canonicalJSON(raw []byte) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("input must contain one JSON value")
		}
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func boundText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}
