package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAReplyIsAppendedToTheNamedSeededConversation(t *testing.T) {
	h := newTestHarness(t)
	handler := h.controlHandler(context.Background())
	host := h.hosts[0]
	if len(host.conversations) != 2 {
		t.Fatalf("seeded %d conversations, want 2", len(host.conversations))
	}
	before, err := os.ReadFile(host.conversations[1].transcript)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/hosts/0/reply", strings.NewReader(`{"conversation":1,"text":"A live reply"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("reply answered %d: %s", response.Code, response.Body)
	}
	var reply struct{ Session string }
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || reply.Session != host.conversations[1].id {
		t.Fatalf("reply named session %q, want %q (%v)", reply.Session, host.conversations[1].id, err)
	}
	after, err := os.ReadFile(host.conversations[1].transcript)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("the reply rewrote the transcript instead of appending to it")
	}
	var record struct {
		Type, SessionID string
		Message         struct {
			Role    string
			Content []struct{ Type, Text string }
		}
	}
	if err := json.Unmarshal(after[len(before):], &record); err != nil {
		t.Fatalf("appended line is not one JSON record: %q %v", after[len(before):], err)
	}
	if record.Type != "assistant" || record.SessionID != reply.Session || record.Message.Role != "assistant" || len(record.Message.Content) != 1 || record.Message.Content[0].Text != "A live reply" {
		t.Fatalf("appended record: %+v", record)
	}

	for _, body := range []string{`{"conversation":2,"text":"x"}`, `{"conversation":0,"text":""}`, `not json`} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/hosts/0/reply", strings.NewReader(body)))
		if response.Code == http.StatusOK {
			t.Errorf("%s was accepted", body)
		}
	}
}
