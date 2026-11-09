package ledger

import "fmt"

func (a WorkAction) AttachCheckpoint() bool {
	return a == WorkAdd || a == WorkClaim || a == WorkSubmit
}

func ProvisionalNote(projectKey string, count int) string {
	if count == 0 {
		return ""
	}
	verb := "are"
	if count == 1 {
		verb = "is"
	}
	return fmt.Sprintf("[intent ledger] %d of the person's messages in %s %s provisional until attached: agenthail ledger --provisional", count, projectKey, verb)
}
