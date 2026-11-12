package ledger

import "time"

const readTriageDefaultIdle = 14 * 24 * time.Hour

func readBacklog(board *Board, view string, req ReadRequest, result map[string]any) (map[string]any, error) {
	limit, err := readLimit(req.Limit, readMaxLimit)
	if err != nil {
		return nil, err
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	rows := []map[string]any{}
	total := 0
	if view == "triage" {
		idle := req.Idle
		if idle == 0 {
			idle = readTriageDefaultIdle
		}
		if idle < 0 {
			return nil, refuse("triage needs a positive idle span")
		}
		items := board.Triage(now, idle)
		total = len(items)
		for _, item := range items[:min(limit, len(items))] {
			rows = append(rows, map[string]any{"id": item.ID, "text": clipText(item.Text, readTextClip), "state": item.State, "clauseId": item.ClauseID, "updatedAt": item.updated()})
		}
		result["triage"], result["idleDays"] = rows, int(idle/(24*time.Hour))
	} else {
		for _, kind := range req.Backlog.Kinds {
			if _, ok := ParseParkKind(string(kind)); !ok {
				return nil, refuse("park kind %q is not one of %s", kind, joinParkKinds())
			}
		}
		entries := board.Backlog(req.Backlog, now, req.Usage)
		total = len(entries)
		for _, entry := range entries[:min(limit, len(entries))] {
			row := map[string]any{"id": entry.Item.ID, "text": clipText(entry.Item.Text, readTextClip), "kind": entry.Item.Park.Kind,
				"summary": entry.Item.Park.Summary(), "ready": entry.Ready, "why": entry.Why, "clauseId": entry.Item.ClauseID}
			if !entry.ReadyAt.IsZero() {
				row["readyAt"] = entry.ReadyAt
			}
			rows = append(rows, row)
		}
		result["backlog"] = rows
	}
	result["total"], result["truncated"] = total, len(rows) < total
	return result, nil
}
