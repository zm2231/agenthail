package daemon

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const (
	promptsDefaultLimit = 50
	promptsMaxLimit     = 200
	promptsMaxQuery     = 200
	promptsScanBatch    = 200
	promptsScanRows     = 2000
	promptsMaxBodyBytes = 64 << 10
)

type promptItem struct {
	SessionID string `json:"sessionId"`
	ItemID    string `json:"itemId"`
	TurnID    string `json:"turnId,omitempty"`
	TS        string `json:"ts"`
	Text      string `json:"text"`
}

// promptsHandler answers GET /api/v1/prompts: what a person typed into this
// host's sessions, newest first, for composer history and prompt search.
// Sessions the journal has not recorded contribute nothing.
func (d *Daemon) promptsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	limit := promptsDefaultLimit
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > promptsMaxLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", promptsMaxLimit))
			return
		}
		limit = parsed
	}
	text := strings.TrimSpace(query.Get("q"))
	if utf8.RuneCountInString(text) > promptsMaxQuery {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", fmt.Sprintf("q must be at most %d characters", promptsMaxQuery))
		return
	}
	before, err := registry.ParseJournalPromptCursor(query.Get("before"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	prompts, next, err := d.typedPrompts(strings.TrimSpace(query.Get("sessionId")), text, before, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("read prompts: %s", err), http.StatusInternalServerError)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"prompts": prompts, "nextBefore": next.String()})
}

// typedPrompts reads journal user messages in batches until it has limit
// typed prompts. It stops after promptsScanRows rows and returns the cursor
// it reached, so a sparse match costs a bounded read per request.
func (d *Daemon) typedPrompts(sessionID, text string, before registry.JournalPromptCursor, limit int) ([]promptItem, registry.JournalPromptCursor, error) {
	prompts := make([]promptItem, 0, limit)
	scanned := 0
	for {
		batch, err := d.Registry.JournalPrompts(registry.JournalPromptQuery{SessionID: sessionID, Text: text, Before: before, Limit: promptsScanBatch})
		if err != nil {
			return nil, registry.JournalPromptCursor{}, err
		}
		for index, row := range batch {
			if prompt, ok := d.typedPrompt(row); ok {
				prompts = append(prompts, prompt)
			}
			if len(prompts) == limit {
				if index == len(batch)-1 && len(batch) < promptsScanBatch {
					return prompts, registry.JournalPromptCursor{}, nil
				}
				return prompts, row.Cursor, nil
			}
		}
		scanned += len(batch)
		if len(batch) < promptsScanBatch {
			return prompts, registry.JournalPromptCursor{}, nil
		}
		before = batch[len(batch)-1].Cursor
		if scanned >= promptsScanRows {
			return prompts, before, nil
		}
	}
}

func (d *Daemon) typedPrompt(row registry.JournalPrompt) (promptItem, bool) {
	body := row.Body
	if row.Truncated && row.BodyRef != "" {
		if full, _, err := d.Registry.SessionJournalBody(row.SessionID, row.BodyRef, 0, promptsMaxBodyBytes); err == nil {
			body = string(full)
		}
	}
	if !isTypedPrompt(body) {
		return promptItem{}, false
	}
	return promptItem{SessionID: row.SessionID, ItemID: row.ItemID, TurnID: row.TurnID, TS: row.TS, Text: body}, true
}

// isTypedPrompt reports whether a user-role message is text a person typed.
// Journal rows written before their provider marked injections carry no
// origin, so the injected wrappers are recognised by their text.
func isTypedPrompt(text string) bool {
	return surfaces.IsTypedHumanPrompt(text)
}
