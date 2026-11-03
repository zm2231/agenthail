package daemon

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

type sessionViewKey struct {
	Activity time.Time `json:"activity"`
	ID       string    `json:"id"`
}

func (a sessionViewKey) before(b sessionViewKey) bool {
	if !a.Activity.Equal(b.Activity) {
		return a.Activity.After(b.Activity)
	}
	return a.ID < b.ID
}

type sessionViewCursor struct {
	Scope   string             `json:"scope"`
	Filters SessionViewFilters `json:"filters"`
	After   sessionViewKey     `json:"after"`
}

func encodeSessionViewCursor(request sessionViewRequest, after sessionViewKey) string {
	encoded, _ := json.Marshal(sessionViewCursor{request.Scope, request.Filters, after})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeSessionViewCursor(raw string) (sessionViewCursor, bool) {
	var cursor sessionViewCursor
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(encoded, &cursor) != nil || cursor.After.ID == "" {
		return cursor, false
	}
	return cursor, true
}

func familiesAfter(families []SessionViewFamily, after *sessionViewKey) []SessionViewFamily {
	if after == nil {
		return families
	}
	for i, family := range families {
		if after.before(family.key) {
			return families[i:]
		}
	}
	return families[len(families):]
}
