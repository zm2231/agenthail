package surface

import (
	"path/filepath"
	"strconv"
	"strings"
)

// IdentifiedBy reports whether selector is session's ID or an ID prefix, its
// PID, the conversation ID its transcript is named after, or the from-session
// id its peer messages carry.
func IdentifiedBy(session Session, selector string) bool {
	if selector == "" {
		return false
	}
	if strings.HasPrefix(session.ID, selector) || (session.PID > 0 && selector == strconv.Itoa(session.PID)) || strings.EqualFold(selector, PeerUUID(session.Surface, session.ID)) {
		return true
	}
	return session.Transcript != "" && selector == strings.TrimSuffix(filepath.Base(session.Transcript), ".jsonl")
}
