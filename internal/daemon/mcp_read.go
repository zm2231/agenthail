package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	mcpItemTextLimit = 4 << 10
	mcpBodyChunk     = 8 << 10
)

type mcpReadInput struct {
	SessionID string  `json:"sessionId" jsonschema:"session id or @alias"`
	Limit     int     `json:"limit,omitempty" jsonschema:"exchanges per page when reading back, 1 to 50; default 5"`
	Before    string  `json:"before,omitempty" jsonschema:"nextCursor from a previous call, to read further back"`
	Since     *uint64 `json:"since,omitempty" jsonschema:"latestSeq from a previous call, to read only what came after it"`
	BodyRef   string  `json:"bodyRef,omitempty" jsonschema:"bodyRef of a clipped item, to page through its full text"`
	Offset    int     `json:"offset,omitempty" jsonschema:"byte offset into bodyRef; use nextOffset from the previous call"`
}

type mcpItemView struct {
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind"`
	Role      string `json:"role,omitempty"`
	Sender    string `json:"sender,omitempty"`
	Title     string `json:"title,omitempty"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
	TurnID    string `json:"turnId,omitempty"`
	Status    string `json:"status,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	BodyRef   string `json:"bodyRef,omitempty"`
}

func (d *Daemon) mcpRead(ctx context.Context, caller *mcpCaller, input mcpReadInput) (any, error) {
	session, err := d.mcpReadableSession(caller, input.SessionID)
	if err != nil {
		return nil, err
	}
	switch {
	case input.BodyRef != "":
		return d.mcpReadBody(session.ID, input.BodyRef, input.Offset)
	case input.Since != nil:
		return d.mcpReadSince(session.ID, *input.Since)
	}
	limit, err := pageLimit(input.Limit, 5, surface.MaxExchangePage)
	if err != nil {
		return nil, err
	}
	return d.mcpReadBack(ctx, session, input.Before, limit)
}

func (d *Daemon) mcpReadBody(sessionID, ref string, offset int) (any, error) {
	if offset < 0 {
		return nil, refuse("invalid", "offset must not be negative.")
	}
	body, total, err := d.Registry.SessionJournalBody(sessionID, ref, offset, offset+mcpBodyChunk)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, refuse("not_found", "That body is no longer retained.")
	}
	if err != nil {
		return nil, refuse("invalid", "The body range is invalid.")
	}
	if offset+len(body) < total {
		body = wholeRunes(body)
	}
	text := string(body)
	end := offset + len(body)
	result := map[string]any{"sessionId": sessionID, "bodyRef": ref, "offset": offset, "end": end, "total": total, "text": text, "truncated": end < total}
	if end < total {
		result["nextOffset"] = end
	}
	return result, nil
}

func (d *Daemon) mcpReadSince(sessionID string, since uint64) (any, error) {
	window, err := d.Registry.SessionJournalAfter(sessionID, since, sessionJournalRetentionCount)
	if err != nil {
		return nil, err
	}
	if window.Gap {
		return nil, &mcpRefusal{Code: "not_found", Message: "Items after that cursor are no longer retained; read back without since.", Detail: map[string]any{"stale": true, "earliestSeq": window.EarliestSeq}}
	}
	toItem := d.journalItemReader(sessionID)
	items := []mcpItemView{}
	used, last := 0, since
	for _, entry := range window.Entries {
		var row sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &row); err != nil {
			return nil, fmt.Errorf("decode journal item: %w", err)
		}
		if journalRowIsItem(row) {
			view := mcpItem(toItem(row))
			encoded, _ := json.Marshal(view)
			if used+len(encoded) > mcpPageBudget && len(items) > 0 {
				break
			}
			used += len(encoded)
			items = append(items, view)
		}
		last = entry.Seq
	}
	latest := max(window.LatestSeq, since)
	return map[string]any{"sessionId": sessionID, "items": items, "latestSeq": last, "truncated": last < latest}, nil
}

func (d *Daemon) mcpReadBack(ctx context.Context, session *surface.Session, cursor string, limit int) (any, error) {
	before, after, err := decodeReadCursor(cursor)
	if err != nil {
		return nil, err
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return nil, refuse("unsupported_surface", "This session's surface is not configured.")
	}
	page, _, err := d.readSessionPage(ctx, session, adapter, before, limit, surfaceOperationTimeout, true)
	var gap *registry.SessionJournalHistoryGapError
	if errors.As(err, &gap) {
		return map[string]any{"sessionId": session.ID, "items": []mcpItemView{}, "truncated": false, "unavailable": "No older history is retained."}, nil
	}
	if err != nil {
		return nil, &mcpRefusal{Code: "busy", Message: "The session could not be read: " + err.Error(), Retryable: true}
	}
	pending := page.Items
	if after != "" {
		index := -1
		for i, item := range pending {
			if item.ID == after {
				index = i
			}
		}
		if index < 0 {
			return nil, &mcpRefusal{Code: "not_found", Message: "The session moved on since that cursor; read again without before.", Detail: map[string]any{"stale": true}}
		}
		pending = pending[:index]
	}
	start, used := len(pending), 0
	for start > 0 {
		encoded, _ := json.Marshal(mcpItem(pending[start-1]))
		if used+len(encoded) > mcpPageBudget && start < len(pending) {
			break
		}
		used += len(encoded)
		start--
	}
	items := make([]mcpItemView, 0, len(pending)-start)
	for _, item := range pending[start:] {
		items = append(items, mcpItem(item))
	}
	result := map[string]any{"sessionId": session.ID, "items": items, "latestSeq": page.JournalSeq, "source": page.Source}
	switch {
	case start > 0:
		result["nextCursor"] = encodeReadCursor(before, pending[start].ID)
	case page.NextBefore > 0:
		result["nextCursor"] = encodeReadCursor(page.NextBefore, "")
	}
	result["truncated"] = result["nextCursor"] != nil
	if page.UnavailableReason != "" {
		result["unavailable"] = page.UnavailableReason
	}
	return result, nil
}

func mcpItem(item surface.TimelineItem) mcpItemView {
	view := mcpItemView{ID: item.ID, Kind: item.Kind, Role: item.Role, Sender: item.Sender, Title: item.Title, Text: item.Text, Timestamp: item.Timestamp, TurnID: item.TurnID, Status: item.Status, Truncated: item.Truncated, BodyRef: item.BodyRef}
	if len(view.Text) > mcpItemTextLimit {
		view.Text = clipUTF8(view.Text, mcpItemTextLimit)
		view.Truncated = true
	}
	return view
}

func clipUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func wholeRunes(body []byte) []byte {
	for start := len(body) - 1; start >= 0 && start >= len(body)-utf8.UTFMax; start-- {
		if utf8.RuneStart(body[start]) {
			if !utf8.FullRune(body[start:]) {
				return body[:start]
			}
			break
		}
	}
	return body
}

func encodeReadCursor(before int64, after string) string {
	return strconv.FormatInt(before, 10) + ":" + after
}

func decodeReadCursor(cursor string) (int64, string, error) {
	if cursor == "" {
		return 0, "", nil
	}
	raw, after, found := strings.Cut(cursor, ":")
	before, err := strconv.ParseInt(raw, 10, 64)
	if !found || err != nil {
		return 0, "", refuse("invalid", "The cursor is invalid; use the nextCursor a previous call returned.")
	}
	return before, after, nil
}
