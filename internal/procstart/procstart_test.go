package procstart

import "testing"

func TestStatStartTimeSkipsCommandNamesWithSpaces(t *testing.T) {
	stat := "13446 (claude (bg) x) S 13433 13433 13433 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 11 0 1562374 1000 50\n"
	if got, err := statStartTime(stat); err != nil || got != "1562374" {
		t.Fatalf("start=%q err=%v", got, err)
	}
	if _, err := statStartTime("13446 claude S"); err == nil {
		t.Fatal("malformed stat accepted")
	}
}
