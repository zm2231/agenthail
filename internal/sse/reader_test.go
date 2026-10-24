package sse

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestReader(t *testing.T) {
	reader := NewReader(strings.NewReader(": ready\nretry: 1500\n\nid: 7\nevent: update\ndata: first\ndata: second\n\ndata: next\n\n"))
	comment, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := comment.Comment, []string{" ready"}; !equalStrings(got, want) {
		t.Fatalf("comments = %#v, want %#v", got, want)
	}
	if !comment.HasRetry || comment.Retry != 1500*time.Millisecond {
		t.Fatalf("retry = %s, want %s", comment.Retry, 1500*time.Millisecond)
	}
	event, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !event.HasID || !event.HasData || event.ID != "7" || event.Type != "update" || event.Data != "first\nsecond" {
		t.Fatalf("event = %#v", event)
	}
	if got, want := reader.Retry(), 1500*time.Millisecond; got != want {
		t.Fatalf("retry = %s, want %s", got, want)
	}
	event, err = reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "7" || !event.HasData || event.Data != "next" {
		t.Fatalf("event = %#v", event)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("error = %v, want EOF", err)
	}
}

func TestReaderAcceptsZeroRetry(t *testing.T) {
	reader := NewReader(strings.NewReader("retry: 0\n\n"))
	event, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !event.HasRetry || event.Retry != 0 {
		t.Fatalf("event = %#v", event)
	}
}

func TestReaderIgnoresInvalidRetry(t *testing.T) {
	for _, retry := range []string{"later", "1.5", "+1", "-1"} {
		t.Run(retry, func(t *testing.T) {
			reader := NewReader(strings.NewReader("retry: " + retry + "\n\ndata: value\n\n"))
			event, err := reader.Next()
			if err != nil {
				t.Fatal(err)
			}
			if event.Data != "value" || reader.Retry() != 0 {
				t.Fatalf("event = %#v, retry = %s", event, reader.Retry())
			}
		})
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
