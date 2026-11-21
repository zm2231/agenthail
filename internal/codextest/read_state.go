package codextest

import "fmt"

type receipt struct {
	firstUnread map[string]any
	revision    int
}

func (r *receipt) json() map[string]any {
	var position any
	if r.firstUnread != nil {
		position = r.firstUnread
	}
	return map[string]any{"firstUnread": position, "revision": fmt.Sprintf("rev-%d", r.revision)}
}

// Like Codex, a thread has a receipt only once it has a turn.
func (d *Daemon) receiptLocked(threadID string) *receipt {
	thread := d.threads[threadID]
	if thread == nil || len(thread.Turns) == 0 {
		return nil
	}
	if d.receipts == nil {
		d.receipts = map[string]*receipt{}
	}
	if d.receipts[threadID] == nil {
		d.receipts[threadID] = &receipt{revision: 1}
	}
	return d.receipts[threadID]
}

func (d *Daemon) receiptJSONLocked(threadID string) any {
	if r := d.receiptLocked(threadID); r != nil {
		return r.json()
	}
	return nil
}

func (d *Daemon) markTurnUnreadLocked(threadID, turnID string) map[string]any {
	r := d.receiptLocked(threadID)
	if r == nil {
		return nil
	}
	if r.firstUnread == nil {
		r.firstUnread = map[string]any{"type": "turn", "turnId": turnID}
	}
	r.revision++
	return map[string]any{"threadId": threadID, "readState": r.json()}
}

func (d *Daemon) updateReadStateLocked(threadID string, params map[string]any) map[string]any {
	r := d.receiptLocked(threadID)
	if r == nil {
		return rpcError("thread has no read state: " + threadID)
	}
	if expected, _ := params["expectedRevision"].(string); expected != r.json()["revision"] {
		return map[string]any{"error": map[string]any{"code": -32600, "message": "read state changed", "data": map[string]any{"reason": "readStateConflict", "readState": r.json()}}}
	}
	operation, _ := params["operation"].(map[string]any)
	switch operation["type"] {
	case "read":
		r.firstUnread = nil
	case "unread":
		r.firstUnread = map[string]any{"type": "threadStart"}
	default:
		return rpcError("unknown read state operation")
	}
	r.revision++
	return map[string]any{"readState": r.json()}
}

// ReadState is the thread's receipt as thread/read reports it, nil before
// its first turn.
func (d *Daemon) ReadState(threadID string) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	state, _ := d.receiptJSONLocked(threadID).(map[string]any)
	return state
}
