package ledger

import "fmt"

// The next session on an item reads only its log, so the holder is told what to put there.
func HolderGuidance(itemID string, mcp bool) string {
	call := fmt.Sprintf(`agenthail ledger log %s "..." --type decision|blocker|result|progress`, itemID)
	if mcp {
		call = fmt.Sprintf(`agenthail_ledger_write {"op":"work","request":{"action":"log","itemId":%q,"text":"...","logType":"decision|blocker|result|progress"}}`, itemID)
	}
	return fmt.Sprintf("Log to %s as you go: each decision and why, blockers, the PR when you open it, test and review results, and what's left. The next session reads only this log. %s", itemID, call)
}
