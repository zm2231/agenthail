package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"time"
)

const (
	sessionBandNeedsYou = iota
	sessionBandActive
	sessionBandIdle
)

type sessionViewKey struct {
	Band int       `json:"band"`
	At   time.Time `json:"at"`
	ID   string    `json:"id"`
}

func (a sessionViewKey) before(b sessionViewKey) bool {
	if a.Band != b.Band {
		return a.Band < b.Band
	}
	if !a.At.Equal(b.At) {
		if a.Band == sessionBandNeedsYou {
			return a.At.Before(b.At)
		}
		return a.At.After(b.At)
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
	if err != nil {
		return cursor, false
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || cursor.After.ID == "" || cursor.After.Band < sessionBandNeedsYou || cursor.After.Band > sessionBandIdle {
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
