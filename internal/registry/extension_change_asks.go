package registry

import (
	"encoding/json"
)

const (
	AskOriginExtensionChange = "extension-change"
	AskOriginExtensionGrant  = "extension-grant"
)

// RewordAsk replaces an open ask's title and questions in place, so a ask
// that rolls up later changes keeps its id and is announced only once.
func (r *Registry) RewordAsk(id, title string, questions []AskQuestion) (bool, error) {
	encoded, err := json.Marshal(questions)
	if err != nil {
		return false, err
	}
	result, err := r.db.Exec(`UPDATE asks SET title=?,questions_json=? WHERE id=? AND state=?`, clip(title, MaxAskTitle), string(encoded), id, AskOpen)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
