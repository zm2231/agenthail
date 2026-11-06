package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestTwoClientsJoiningAReplyAtDifferentTimesThroughAPairedHostBothRebuildIt(t *testing.T) {
	local, remote, record := pairedFederation(t)
	server := httptest.NewServer(local.handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := server.URL + "/api/v1/session-stream?id=" + url.QueryEscape(hosts.Qualify(record.ID, "remote-a")) + "&after=0"
	early := openStreamClient(t, ctx, stream)
	var source *sessionSource
	testkit.Eventually(t, "the remote host follows the session for the relayed client", 10*time.Second, func() bool {
		remote.daemon.sources.mu.Lock()
		defer remote.daemon.sources.mu.Unlock()
		source = remote.daemon.sources.sources["remote-a"]
		return source != nil
	})
	const key = "turn-1:text"
	var want strings.Builder
	send := func(text string) {
		version := uint64(want.Len() + 1)
		want.WriteString(text)
		source.append(surface.StreamEvent{ID: key, ProviderKey: key, Version: version, Operation: "append", TurnID: "turn-1", Kind: "text", Role: "assistant", Text: text})
	}
	for index := 0; want.Len() < 2*sessionStreamBodyBytes; index++ {
		send(fmt.Sprintf("relayed %d é ", index))
	}
	early.waitFor(t, "early client past the inline body", func(row sessionstream.Event) bool { return row.Body == want.String() })
	late := openStreamClient(t, ctx, stream)
	for index := 0; want.Len() < 3*sessionStreamBodyBytes; index++ {
		send(fmt.Sprintf("later %d ü ", index))
	}
	reply := want.String()
	for _, client := range []struct {
		label  string
		stream *streamClient
	}{{"early", early}, {"late", late}} {
		client.stream.waitFor(t, client.label+" client through the hub", func(row sessionstream.Event) bool { return row.Body == reply })
	}
	cancel()
	<-early.done
	<-late.done
	for _, client := range []struct {
		label  string
		stream *streamClient
	}{{"early", early}, {"late", late}} {
		types := client.stream.types[key]
		if len(types) < 2 || types[0] != "item" || types[len(types)-1] != "append" {
			t.Fatalf("%s client through the hub saw %v, want the reply whole first and appends after", client.label, types[:min(len(types), 4)])
		}
	}
}
