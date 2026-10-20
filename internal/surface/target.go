package surface

import (
	"path/filepath"
	"strconv"
	"strings"
)

// IdentifiedBy reports whether selector is session's ID or an ID prefix, its
// PID, or the conversation ID its transcript is named after.
func IdentifiedBy(session Session, selector string) bool {
	if selector == "" {
		return false
	}
	if strings.HasPrefix(session.ID, selector) || (session.PID > 0 && selector == strconv.Itoa(session.PID)) {
		return true
	}
	return session.Transcript != "" && selector == strings.TrimSuffix(filepath.Base(session.Transcript), ".jsonl")
}
