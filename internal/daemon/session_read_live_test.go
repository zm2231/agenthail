package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	providers "github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestLiveSessionLatestReads(t *testing.T) {
	input, binary, home := os.Getenv("AGENTHAIL_LIVE_READ_SESSION"), os.Getenv("AGENTHAIL_LIVE_READ_CLI"), os.Getenv("AGENTHAIL_LIVE_READ_HOME")
	if testing.Short() || input == "" || binary == "" || home == "" {
		t.Skip("requires explicit live session JSON, built CLI and read-only provider home")
	}
	var session surface.Session
	if err := json.Unmarshal([]byte(input), &session); err != nil {
		t.Fatal(err)
	}
	if session.Surface != surface.KindClaude || session.Transcript == "" {
		t.Fatal("requires a Claude session with a local transcript")
	}
	adapter := providers.NewClaude("Default", home)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	current, err := adapter.ReadSession(ctx, &session, surface.SessionReadRequest{Limit: 40})
	if err != nil || current == nil || len(current.Items) < 2 {
		t.Fatalf("live read error=%v", err)
	}
	latest := ""
	for _, item := range current.Items {
		if item.Role == "assistant" && (item.Kind == "message" || item.Kind == "text") {
			latest = item.Text
		}
	}
	if latest == "" || current.Items[0].Text == latest {
		t.Fatal("live transcript must have an assistant answer newer than its oldest page item")
	}
	pid, err := os.ReadFile(filepath.Join(home, ".agenthail", "daemon.pid"))
	if err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []string{"cli", "voice"} {
		t.Run(consumer, func(t *testing.T) {
			privateHome := t.TempDir()
			store, err := registry.Open(filepath.Join(privateHome, ".agenthail", "registry.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.RegisterSession(session); err != nil {
				t.Fatal(err)
			}
			d := New(store, []surface.Surface{adapter})
			defer d.sources.shutdown()
			seed := &sessionSource{manager: d.sources, session: session, epoch: "live-stale", appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
			seed.appendSeedItems(current.Items[:1])
			if err := store.MarkSessionJournalSeedWithIdentity(session.ID, true, current.TranscriptIdentity); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			if consumer == "voice" {
				events, err := (daemonVoiceStreamProvider{daemon: d}).ReadVoiceSession(ctx, &session)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, event := range events {
					found = found || event.Body == latest
				}
				if !found {
					t.Fatal("voice read omitted actual latest assistant answer")
				}
				t.Logf("live voice producer: latest assistant matched, %d items, %s", len(events), time.Since(started))
				return
			}
			server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "live-read-private-token"}))
			defer server.Close()
			t.Setenv("HOME", privateHome)
			if err := SaveDashboardConfig(DashboardConfig{Enabled: true, Listen: strings.TrimPrefix(server.URL, "http://")}); err != nil {
				t.Fatal(err)
			}
			for path, data := range map[string][]byte{DashboardTokenPath(): []byte("live-read-private-token"), PidFilePath(): pid} {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(filepath.Join(home, ".claude"), filepath.Join(privateHome, ".claude")); err != nil {
				t.Fatal(err)
			}
			if err := store.SetAlias("live-read-target", session.ID); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, binary, "last", "@live-read-target", "5", "--full", "--json")
			output, err := command.Output()
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					t.Fatalf("CLI: %s: %s", err, exit.Stderr)
				}
				t.Fatal(err)
			}
			var result struct {
				Source    string             `json:"source"`
				ReadError string             `json:"readError"`
				Exchanges []surface.Exchange `json:"exchanges"`
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatal(err)
			}
			latestCLIAnswer := ""
			for _, exchange := range result.Exchanges {
				if exchange.Assistant != "" {
					latestCLIAnswer = exchange.Assistant
				}
			}
			if result.Source != "journal" || result.ReadError != "" || latestCLIAnswer != latest {
				t.Fatalf("CLI did not return live latest answer: source=%s readError=%s exchanges=%d", result.Source, result.ReadError, len(result.Exchanges))
			}
			t.Logf("live CLI last 5 --full --json: latest assistant matched, %s", time.Since(started))
		})
	}
}
