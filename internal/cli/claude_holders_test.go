package cli

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestAgentSendToAConversationOpenInTwoTerminalsNamesThePinsAndAPinReachesOne(t *testing.T) {
	fixture := claudetest.New(t)
	const conversation = "conversation-two-terminals"
	fixture.Transcript(conversation, fixture.Home)
	first := fixture.Holder(conversation, fixture.Home)
	later := fixture.Holder(conversation, fixture.Home)
	peers := claudetest.ListenPeerManager(t, fixture.Home)
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	app.Surfaces[0].Surface = surfaces.NewClaude(nil, fixture.Home)

	listed, err := captureStdout(t, func() error { return app.Run([]string{"list"}) })
	if err != nil || !strings.Contains(listed, fmt.Sprintf("2 terminals: %d", first.PID)) || !strings.Contains(listed, strconv.Itoa(later.PID)) {
		t.Fatalf("list=%q err=%v, want both terminals named", listed, err)
	}

	err = app.Run([]string{"send", conversation, "which terminal?"})
	if err == nil {
		t.Fatal("a send to a conversation open in two terminals was not refused")
	}
	for _, pid := range []int{first.PID, later.PID} {
		if !strings.Contains(err.Error(), fmt.Sprintf("claude:%d", pid)) {
			t.Fatalf("refusal %q does not name the pin for process %d", err, pid)
		}
	}
	if sends := peers.Sends(); len(sends) != 0 {
		t.Fatalf("a refused send was delivered: %+v", sends)
	}

	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"send", "claude:" + strconv.Itoa(later.PID), "to the later terminal"})
	}); err != nil {
		t.Fatal(err)
	}
	sends := peers.Sends()
	if len(sends) != 1 || sends[0].Target != later.Socket() || sends[0].Message != "to the later terminal" {
		t.Fatalf("sends=%+v, want one to %s", sends, later.Socket())
	}
	if row, err := r.Session(conversation); err != nil || row.PID != 0 {
		t.Fatalf("row=%+v err=%v, want a pinned send to leave the conversation unbound", row, err)
	}
}
