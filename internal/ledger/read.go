package ledger

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/apitime"
)

const (
	readDefaultLimit = 25
	readMaxLimit     = 100
	readMaxReady     = 50
	readProvisional  = 5
	readTextClip     = 240
	readReplyClip    = 600
)

var readViews = []string{"board", "goals", "asks", "decisions", "questions", "work", "ready", "item"}

type ReadRequest struct {
	View   string
	ID     string
	Limit  int
	Cursor string
	Work   *WorkQuery
	Budget int
	Sync   *SyncHealth
	Sorter *SorterHealth
}

type SorterHealth struct {
	Model    string    `json:"model"`
	Since    time.Time `json:"unavailableSince,omitzero"`
	Error    string    `json:"error,omitempty"`
	Failures int       `json:"failures,omitempty"`
	Dropped  int       `json:"dropped,omitempty"`
}

// SyncHealth is what the caller's host last saw syncing a ledger kept on another host. It holds no times, so a sync that changes nothing leaves the read the same.
type SyncHealth struct {
	OwnerHead int64
	Error     string
}

type ledgerEntry struct {
	id  string
	raw json.RawMessage
}

func (s *Store) Read(projectKey string, req ReadRequest) (map[string]any, error) {
	view := cmp.Or(req.View, "board")
	if !slices.Contains(readViews, view) {
		return nil, refuse("unknown view %q; use %s", view, strings.Join(readViews, ", "))
	}
	board, err := s.Board(projectKey)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"projectKey": projectKey, "view": view}
	switch view {
	case "board":
		return s.readBoard(projectKey, board, req, result)
	case "item":
		return readItem(board, req.ID, result)
	case "ready":
		limit, err := readLimit(req.Limit, readMaxReady)
		if err != nil {
			return nil, err
		}
		ready := board.Ready()
		rows := []map[string]any{}
		for rank, item := range ready[:min(limit, len(ready))] {
			rows = append(rows, map[string]any{"rank": rank + 1, "id": item.ID, "text": clipText(item.Text, readTextClip), "state": item.State, "assignee": item.Assignee})
		}
		result["ready"], result["total"], result["truncated"] = rows, len(ready), len(rows) < len(ready)
		return result, nil
	}
	limit, err := readLimit(req.Limit, readMaxLimit)
	if err != nil {
		return nil, err
	}
	var list any
	switch view {
	case "goals":
		list = board.Goals()
	case "asks":
		list = board.AllAsks()
	case "decisions":
		list = board.Decisions()
	case "questions":
		list = board.Questions()
	case "work":
		if req.Work == nil {
			list = board.WorkItems()
			break
		}
		q := *req.Work
		if q.Sort != "" && q.Sort != "id" {
			return nil, refuse("the work view pages by id; sort %q is not available here", q.Sort)
		}
		q.Sort, q.Limit = "id", 0
		if list, err = board.QueryWork(q); err != nil {
			return nil, err
		}
	}
	entries, next := pageEntries(entriesOf(list), req.Cursor, limit, req.Budget)
	result[view], result["truncated"] = entries, next != ""
	if next != "" {
		result["nextCursor"] = next
	}
	return result, nil
}

func (s *Store) readBoard(projectKey string, board *Board, req ReadRequest, result map[string]any) (map[string]any, error) {
	counts := map[string]int{
		"goals":     len(board.Goals()),
		"asks":      len(board.AllAsks()),
		"decisions": len(board.Decisions()),
		"questions": len(board.Questions()),
		"work":      len(board.WorkItems()),
		"ready":     len(board.Ready()),
	}
	attachment, err := s.Attachment(projectKey, board)
	if err != nil {
		return nil, err
	}
	sync, err := s.Status(projectKey)
	if err != nil {
		return nil, err
	}
	syncView := map[string]any{"owner": sync.Owner, "cursor": sync.Cursor, "unreplayed": sync.Unreplayed}
	if h := req.Sync; h != nil && sync.Owner != "" {
		if h.OwnerHead > 0 {
			syncView["ownerHead"], syncView["behind"] = h.OwnerHead, max(0, h.OwnerHead-sync.Cursor)
		}
		if h.Error != "" {
			syncView["error"] = h.Error
		}
	}
	scope := board.Capture()
	capture := map[string]any{"state": captureName(scope.State)}
	if scope.State == LedgerEnabled {
		capture["since"], capture["attach"] = scope.Since, scope.Attach
	}
	provisional := attachment.Provisional
	newest := slices.Clone(provisional[max(0, len(provisional)-readProvisional):])
	for i := range newest {
		newest[i].Context = clipReply(newest[i].Context)
	}
	goals, next := pageEntries(entriesOf(board.Goals()), "", readDefaultLimit, req.Budget)
	result["appliedSeq"] = board.AppliedSeq()
	result["capture"], result["sync"], result["counts"] = capture, syncView, counts
	result["provisional"] = map[string]any{"total": len(provisional), "newest": readJSON(newest)}
	if stats := board.SortStats(); stats.Filed > 0 {
		result["autoFiled"] = stats
	}
	if req.Sorter != nil {
		result["sorter"] = readJSON(req.Sorter)
	}
	result["goals"], result["truncated"] = goals, next != ""
	if next != "" {
		result["nextCursor"], result["nextView"] = next, "goals"
	}
	return result, nil
}

func readItem(board *Board, id string, result map[string]any) (map[string]any, error) {
	if id == "" {
		return nil, refuse("the item view needs an id: a work item, a work action or an ask")
	}
	if ctx, ok := board.WorkContext(id); ok {
		result["kind"], result["item"] = "work", readJSON(ctx)
		return result, nil
	}
	for _, ask := range board.AllAsks() {
		if ask.ID == id {
			result["kind"], result["item"] = "ask", readJSON(ask)
			return result, nil
		}
	}
	return nil, &NotFound{What: id}
}

type NotFound struct{ What string }

func (n *NotFound) Error() string { return n.What + " is not a work item or an ask in this project" }

func captureName(state int) string {
	switch state {
	case LedgerEnabled:
		return "enabled"
	case LedgerDisabled:
		return "disabled"
	}
	return "lead"
}

func readLimit(requested, most int) (int, error) {
	if requested == 0 {
		return min(readDefaultLimit, most), nil
	}
	if requested < 1 || requested > most {
		return 0, refuse("limit must be between 1 and %d", most)
	}
	return requested, nil
}

func readJSON(value any) json.RawMessage {
	encoded, _ := apitime.Marshal(value)
	return encoded
}

func clipText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !isRuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func clipReply(context *ContextPayload) *ContextPayload {
	if context == nil || context.Reply == nil || len(context.Reply.Text) <= readReplyClip {
		return context
	}
	clipped, reply := *context, *context.Reply
	start := len(reply.Text) - readReplyClip
	for start < len(reply.Text) && !isRuneStart(reply.Text[start]) {
		start++
	}
	reply.Text = reply.Text[start:]
	clipped.Reply = &reply
	return &clipped
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func entriesOf(list any) []ledgerEntry {
	var raw []json.RawMessage
	_ = json.Unmarshal(readJSON(list), &raw)
	entries := make([]ledgerEntry, 0, len(raw))
	for _, item := range raw {
		var identity struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(item, &identity)
		entries = append(entries, ledgerEntry{id: identity.ID, raw: item})
	}
	slices.SortFunc(entries, func(a, b ledgerEntry) int { return CompareIDs(a.id, b.id) })
	return entries
}

// Ids end in a per-kind counter, so this is the order entries were made.
func CompareIDs(a, b string) int {
	headA, numberA := splitID(a)
	headB, numberB := splitID(b)
	return cmp.Or(strings.Compare(headA, headB), cmp.Compare(numberA, numberB), strings.Compare(a, b))
}

func splitID(id string) (string, int) {
	digits := len(id)
	for digits > 0 && id[digits-1] >= '0' && id[digits-1] <= '9' {
		digits--
	}
	number, _ := strconv.Atoi(id[digits:])
	return id[:digits], number
}

// A page holds at least one entry even past the budget, so paging always advances.
func pageEntries(entries []ledgerEntry, cursor string, limit, budget int) ([]json.RawMessage, string) {
	start := 0
	if cursor != "" {
		start = len(entries)
		for index, entry := range entries {
			if CompareIDs(entry.id, cursor) > 0 {
				start = index
				break
			}
		}
	}
	page := []json.RawMessage{}
	used, next := 0, start
	for ; next < len(entries) && len(page) < limit; next++ {
		if budget > 0 && used+len(entries[next].raw) > budget && len(page) > 0 {
			break
		}
		used += len(entries[next].raw)
		page = append(page, entries[next].raw)
	}
	if next < len(entries) {
		return page, entries[next-1].id
	}
	return page, ""
}
