package registry

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func channelledMoveRegistry(t *testing.T) *Registry {
	t.Helper()
	r := moveRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "second", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"builders", "reviewers"} {
		if _, err := r.CreateChannel(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AddToChannel("builders", moveConversation); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAMoveIsRefusedWhenItsChannelsChangeAfterItDeclaredThem(t *testing.T) {
	r := channelledMoveRegistry(t)
	declared := carryOf(t, r)
	if len(declared.Channels) != 1 || declared.Channels[0] != (MovedChannel{Name: "builders", Home: ""}) {
		t.Fatalf("declared channels=%+v", declared.Channels)
	}
	if err := r.AddToChannel("reviewers", moveConversation); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), declared, ""); err == nil || !strings.Contains(err.Error(), "channels of") {
		t.Fatalf("a move that declared one of two channels began: %v", err)
	}
}

func TestTheChannelsOfAMovingSessionCannotChange(t *testing.T) {
	r := channelledMoveRegistry(t)
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), carryOf(t, r), ""); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func() error{
		"join":  func() error { return r.AddToChannel("reviewers", moveConversation) },
		"leave": func() error { return r.RemoveFromChannel("builders", moveConversation) },
	} {
		if err := change(); err == nil || !strings.Contains(err.Error(), "is moving to another host") {
			t.Fatalf("%s: the channels of a moving session changed: %v", name, err)
		}
	}
	if members, err := r.ChannelMembers("builders"); err != nil || len(members) != 1 {
		t.Fatalf("members=%v err=%v", members, err)
	}
	if err := r.AddToChannel("reviewers", "second"); err != nil {
		t.Fatalf("a session that is not moving could not join: %v", err)
	}
}

func TestAMovedSessionKeepsTheChannelsOfItsFormerHostUntilItMovesOn(t *testing.T) {
	const self, home = "h0123456789ef", "h0123456789cd"
	r := openTestRegistry(t)
	for _, host := range []PairedHost{{ID: home, Name: "home", Endpoint: "https://home.example.ts.net:7412", Address: "100.64.0.3", Token: "ahd_home"}, {ID: moveTarget, Name: "server", Endpoint: "https://server.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_server"}} {
		if _, err := r.AddHost(host); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.CreateChannel("local"); err != nil {
		t.Fatal(err)
	}
	carried := MoveCarry{Channels: []MovedChannel{{Name: "builders", Home: home}, {Name: "local", Home: self}}}
	if err := r.CheckMovedChannels(moveConversation, self, []MovedChannel{{Name: "elsewhere", Home: "h-unpaired"}}); err == nil || !strings.Contains(err.Error(), "not paired here") {
		t.Fatalf("a channel kept on an unpaired host was accepted: %v", err)
	}
	if _, err := r.PrepareMoveIn("move-in", moveConversation, home, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-in", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 7, Transcript: "/tmp/t.jsonl", HasLocal: true}
	if _, err := r.TakeMovedSession("move-in", session, "", time.Time{}, nil, carried, self); err != nil {
		t.Fatal(err)
	}
	if got, found, err := r.RemoteChannel(moveConversation, "builders"); err != nil || !found || got != home {
		t.Fatalf("#builders kept at %q found=%v err=%v", got, found, err)
	}
	if _, found, _ := r.RemoteChannel(moveConversation, "local"); found {
		t.Fatal("a channel this host keeps was recorded as another host's")
	}
	onward, err := r.MoveCarry(moveConversation, self)
	if err != nil || len(onward.Channels) != 1 || onward.Channels[0] != (MovedChannel{Name: "builders", Home: home}) {
		t.Fatalf("moving on carries channels %+v err=%v", onward.Channels, err)
	}
	if err := r.BeginMoveOut("move-out", moveConversation, moveTarget, []byte(`{}`), onward, self); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-out", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompleteMoveOut("move-out", testPlaceholder); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := r.RemoteChannel(moveConversation, "builders"); found {
		t.Fatal("the host the session left still posts for it to #builders")
	}
}

func TestAChannelNameNamesOneChannelForASession(t *testing.T) {
	const self = "h0123456789ef"
	r := channelledMoveRegistry(t)
	if err := r.CheckMovedChannels(moveConversation, self, []MovedChannel{{Name: "builders", Home: moveTarget}}); err == nil || !strings.Contains(err.Error(), "this host's own #builders") {
		t.Fatalf("a session in this host's #builders took #builders of another host: %v", err)
	}
	if err := r.CheckMovedChannels("second", self, []MovedChannel{{Name: "ops", Home: self}, {Name: "ops", Home: moveTarget}}); err == nil || !strings.Contains(err.Error(), "two channels named #ops") {
		t.Fatalf("a move with two channels of one name was accepted: %v", err)
	}
	if _, err := r.db.Exec(`INSERT INTO remote_channels(channel_name,home_host_id,session_id) VALUES('reviewers',?,?)`, moveTarget, moveConversation); err != nil {
		t.Fatal(err)
	}
	if err := r.AddToChannel("reviewers", moveConversation); err == nil || !strings.Contains(err.Error(), "already in #reviewers on host") {
		t.Fatalf("a session in another host's #reviewers joined this host's: %v", err)
	}
	if _, err := r.db.Exec(`INSERT INTO remote_channels(channel_name,home_host_id,session_id) VALUES('builders',?,?)`, moveTarget, moveConversation); err != nil {
		t.Fatal(err)
	}
	if reason, err := r.SessionMoveRefusal(moveConversation, true); err != nil || !strings.Contains(reason, "two channels named #builders") {
		t.Fatalf("refusal=%q err=%v, want the session's two #builders named", reason, err)
	}
}
