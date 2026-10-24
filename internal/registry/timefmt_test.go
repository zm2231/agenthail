package registry

import (
	"testing"
	"time"
)

func TestTimestampRoundTrip(t *testing.T) {
	value := time.Date(2026, 10, 24, 1, 2, 3, 4, time.FixedZone("offset", -5*60*60))
	encoded := formatTimestamp(value)
	if encoded != "2026-10-24T06:02:03.000000004Z" {
		t.Fatalf("formatTimestamp() = %q", encoded)
	}
	decoded, err := parseTimestamp(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(value) {
		t.Fatalf("parseTimestamp() = %s, want %s", decoded, value)
	}
}

func TestParseSQLiteTimestamp(t *testing.T) {
	value, err := parseSQLiteTimestamp("2026-10-24 01:02:03")
	if err != nil {
		t.Fatal(err)
	}
	if got := formatSQLiteTimestamp(value); got != "2026-10-24 01:02:03" {
		t.Fatalf("formatSQLiteTimestamp() = %q", got)
	}
}
