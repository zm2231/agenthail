package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestJSONOutputUsesTheAPITimestampFormat(t *testing.T) {
	target := surface.Session{ID: "target", Name: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusBusy, LastActive: time.Now()}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{target.ID: target}, listed: []surface.Session{target}}
	app, r := cliFixture(t, fake)
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Truncate(time.Second)
	if _, err := r.QueueMessageWithOptions(target.ID, "after this turn", "", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	document, err := captureStdout(t, func() error { return app.Run([]string{"queue", "list", "--json"}) })
	if err != nil {
		t.Fatalf("queue list --json=%s err=%v", document, err)
	}
	var listed struct {
		Messages []struct {
			QueuedAt string `json:"queuedAt"`
			Message  string `json:"message"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(document), &listed); err != nil || len(listed.Messages) != 1 {
		t.Fatalf("queue list --json=%s err=%v", document, err)
	}
	queuedAt := listed.Messages[0].QueuedAt
	parsed, err := time.Parse("2006-01-02T15:04:05.000000000Z", queuedAt)
	if err != nil {
		t.Fatalf("queuedAt %q is not the API's UTC RFC 3339 layout: %v", queuedAt, err)
	}
	if parsed.Before(before) || parsed.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("queuedAt %s is not the time the message was queued (%s)", parsed, before)
	}
	if listed.Messages[0].Message != "after this turn" {
		t.Fatalf("message changed: %q", listed.Messages[0].Message)
	}
}
