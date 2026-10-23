package sse

import (
	"net/http/httptest"
	"testing"
)

func TestCursorPrefersLastEventIDOverAfter(t *testing.T) {
	cases := []struct {
		name, lastID, query string
		want                uint64
		invalid             bool
	}{
		{name: "reconnect", lastID: "7", query: "?after=3", want: 7},
		{name: "first connect", query: "?after=3", want: 3},
		{name: "no cursor", want: 0},
		{name: "blank header falls back", lastID: "  ", query: "?after=4", want: 4},
		{name: "garbage", query: "?after=x", invalid: true},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/stream"+c.query, nil)
		if c.lastID != "" {
			r.Header.Set("Last-Event-ID", c.lastID)
		}
		got, err := Cursor(r)
		if c.invalid {
			if err == nil {
				t.Errorf("%s: cursor %d accepted", c.name, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %d, %v; want %d", c.name, got, err, c.want)
		}
	}
}
