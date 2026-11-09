package registry

import (
	"encoding/json"
	"slices"
	"strconv"
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
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), declared, ""); err == nil || !strings.Contains(err.Error(), "changed while the move was prepared") {
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

func registryWithHosts(t *testing.T, hosts ...string) *Registry {
	t.Helper()
	r := openTestRegistry(t)
	for index, id := range hosts {
		host := PairedHost{ID: id, Name: "host-" + id, Endpoint: "https://" + id + ".example.ts.net:7412", Address: "100.64.0." + strconv.Itoa(index+2), Token: "ahd_" + id}
		if _, err := r.AddHost(host); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func takeMoved(t *testing.T, r *Registry, from, self string, carry MoveCarry) {
	t.Helper()
	if _, err := r.PrepareMoveIn("move-in", moveConversation, from, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-in", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 7, Transcript: "/tmp/t.jsonl", HasLocal: true}
	if _, err := r.TakeMovedSession("move-in", session, "", time.Time{}, nil, carry, self, MovedCoordinatorPlace{}); err != nil {
		t.Fatal(err)
	}
}

func TestSameNamedChannelsOnTwoHostsBothMoveOnWithTheSession(t *testing.T) {
	const home, here, next = "h0123456789cd", "h0123456789ef", "h0123456789aa"
	r := registryWithHosts(t, home, next)
	if err := r.CheckMovedChannels(here, []MovedChannel{{Name: "elsewhere", Home: "h0123456789bb"}}); err == nil || !strings.Contains(err.Error(), "not paired here") {
		t.Fatalf("a channel kept on an unpaired host was accepted: %v", err)
	}
	if _, err := r.CreateChannel("builders"); err != nil {
		t.Fatal(err)
	}
	takeMoved(t, r, home, here, MoveCarry{Channels: []MovedChannel{{Name: "builders", Home: home}, {Name: "builders", Home: here}}})
	if homes, err := r.RemoteChannelHomes(moveConversation, "builders"); err != nil || !slices.Equal(homes, []string{home}) {
		t.Fatalf("#builders kept on %v err=%v, want only the former host's recorded", homes, err)
	}
	if err := r.AddToChannel("builders", moveConversation); err != nil {
		t.Fatalf("a session in another host's #builders could not join this host's: %v", err)
	}
	onward, err := r.MoveCarry(moveConversation, here)
	if err != nil || !slices.Equal(onward.Channels, []MovedChannel{{Name: "builders", Home: home}, {Name: "builders", Home: here}}) {
		t.Fatalf("moving on carries channels %+v err=%v", onward.Channels, err)
	}

	beyond := registryWithHosts(t, home, here)
	takeMoved(t, beyond, here, next, onward)
	if homes, err := beyond.RemoteChannelHomes(moveConversation, "builders"); err != nil || !slices.Equal(homes, []string{home, here}) {
		t.Fatalf("the next host keeps #builders on %v err=%v, want both hosts'", homes, err)
	}

	if err := r.BeginMoveOut("move-out", moveConversation, next, []byte(`{}`), onward, here); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-out", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompleteMoveOut("move-out", MovePlaceholder{EntityID: next + "::" + moveConversation, Projection: json.RawMessage(`{"id":"` + next + "::" + moveConversation + `","surface":"claude"}`), UnavailableReason: "moved"}); err != nil {
		t.Fatal(err)
	}
	if homes, _ := r.RemoteChannelHomes(moveConversation, "builders"); len(homes) != 0 {
		t.Fatalf("the host the session left still posts for it to #builders on %v", homes)
	}
	if members, err := r.ChannelMembers("builders"); err != nil || len(members) != 1 {
		t.Fatalf("this host's #builders members=%v err=%v, want the session kept", members, err)
	}
}
