package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/sse"
	"github.com/zm2231/agenthail/internal/surface"
)

type streamClient struct {
	rows  chan sessionstream.Event
	types map[string][]string
	wire  int
	done  chan struct{}
}

func openStreamClient(t *testing.T, ctx context.Context, url string) *streamClient {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	client := &streamClient{rows: make(chan sessionstream.Event, 1<<14), types: map[string][]string{}, done: make(chan struct{})}
	go func() {
		defer close(client.done)
		defer response.Body.Close()
		reader := sse.NewReader(response.Body)
		rebuild := sessionstream.NewRebuild()
		for {
			event, err := reader.Next()
			if err != nil {
				return
			}
			if !event.HasData {
				continue
			}
			client.wire += len(event.Data)
			var envelope struct {
				Type   string `json:"type"`
				ItemID string `json:"itemId"`
				Data   struct {
					ItemID string `json:"itemId"`
				} `json:"data"`
			}
			if json.Unmarshal([]byte(event.Data), &envelope) != nil {
				return
			}
			item := envelope.ItemID + envelope.Data.ItemID
			client.types[item] = append(client.types[item], envelope.Type)
			seq, _ := strconv.ParseUint(event.ID, 10, 64)
			row, err := rebuild.Apply(seq, []byte(event.Data))
			if err != nil {
				client.rows <- sessionstream.Event{Kind: "rebuild-error", Reason: err.Error()}
				return
			}
			client.rows <- row
		}
	}()
	return client
}

func (c *streamClient) waitFor(t *testing.T, label string, done func(sessionstream.Event) bool) sessionstream.Event {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case row := <-c.rows:
			if row.Kind == "rebuild-error" {
				t.Fatalf("%s: %s", label, row.Reason)
			}
			if done(row) {
				return row
			}
		case <-deadline:
			t.Fatalf("%s: the client never saw the row it waited for", label)
		}
	}
}

func TestAReplyPastTheInlineBodyKeepsGrowingOnEveryStreamClient(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 1)}
	adapter.caps.Stream = true
	d := newTestDaemon(t, reg, []surface.Surface{adapter})
	d.sources = newTestSourceManager(t, reg)
	server := httptest.NewServer(dashboardRouter(d))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	url := server.URL + "/api/v1/session-stream?id=" + from.ID + "&after=0"
	early := openStreamClient(t, ctx, url)
	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("source did not start")
	}
	const key = "turn-1:text"
	var want strings.Builder
	send := func(text string, final bool) {
		version := uint64(want.Len() + 1)
		want.WriteString(text)
		adapter.events <- surface.StreamEvent{ID: key, ProviderKey: key, Version: version, Operation: "append", Final: final, TurnID: "turn-1", Kind: "text", Role: "assistant", Text: text}
	}
	for index := 0; want.Len() < 2*sessionStreamBodyBytes; index++ {
		send(fmt.Sprintf("delta %d é ", index), false)
	}
	early.waitFor(t, "early client at two inline bodies", func(row sessionstream.Event) bool { return row.Body == want.String() })
	late := openStreamClient(t, ctx, url)
	for index := 0; want.Len() < 4*sessionStreamBodyBytes; index++ {
		send(fmt.Sprintf("more %d ü ", index), false)
	}
	send("end", false)
	reply := want.String()
	adapter.events <- surface.StreamEvent{ID: key, ProviderKey: key, Version: 1 << 30, Operation: "upsert", Final: true, TurnID: "turn-1", Kind: "text", Role: "assistant", Text: reply}
	for _, client := range []struct {
		label  string
		stream *streamClient
	}{{"early", early}, {"late", late}} {
		row := client.stream.waitFor(t, client.label, func(row sessionstream.Event) bool { return row.Final && row.Body == reply })
		if row.Truncated {
			t.Fatalf("%s client holds the whole reply but marks it truncated", client.label)
		}
	}
	adapter.events <- surface.StreamEvent{ID: key, ProviderKey: key, Version: 1<<30 + 1, Operation: "upsert", Final: true, TurnID: "turn-1", Kind: "text", Role: "assistant", Text: "rewritten"}
	for _, client := range []*streamClient{early, late} {
		client.waitFor(t, "rewrite", func(row sessionstream.Event) bool { return row.Body == "rewritten" })
	}
	cancel()
	<-early.done
	<-late.done
	for _, client := range []struct {
		label  string
		stream *streamClient
	}{{"early", early}, {"late", late}} {
		types := client.stream.types[key]
		if len(types) < 3 || types[0] != "item" || types[len(types)-2] != "append" || types[len(types)-1] != "item" {
			t.Fatalf("%s client envelopes for the reply=%v, want a whole row first, appends through the final reply, and a whole row for the rewrite", client.label, types)
		}
	}
	if envelopes := len(early.types[key]); early.wire/envelopes > 512 {
		t.Fatalf("the early client received %d bytes in %d envelopes for a %d byte reply", early.wire, envelopes, len(reply))
	}
}
