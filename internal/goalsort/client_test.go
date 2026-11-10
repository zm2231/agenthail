package goalsort_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/goalsort"
)

func TestTheRequestBodyMatchesTheMeasuredSetup(t *testing.T) {
	body, err := goalsort.Body("d1-3b", goalsort.Question{
		Message:       strings.Repeat("m", 6100),
		PreviousReply: strings.Repeat("a", 100) + strings.Repeat("z", 2000),
		Goals:         []goalsort.Goal{{ID: "widget-G1", Name: "search", Requests: []string{"rank exact matches first"}}, {ID: "widget-G2", Name: "billing"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model string `json:"model"`
		State struct {
			Message string `json:"message"`
			Reply   string `json:"agent_previous_reply"`
		} `json:"state"`
		Questions map[string]struct {
			Type         string                     `json:"type"`
			Instructions string                     `json:"instructions"`
			Criteria     map[string]json.RawMessage `json:"criteria"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "d1-3b" || len(got.State.Message) != 6000 || got.State.Reply != strings.Repeat("z", 2000) {
		t.Fatalf("model %q, message %d chars, reply %d chars", got.Model, len(got.State.Message), len(got.State.Reply))
	}
	goal, ok := got.Questions["goal"]
	if !ok || len(got.Questions) != 1 || goal.Type != "choice" {
		t.Fatalf("questions = %+v", got.Questions)
	}
	if want := "Which project goal is the person's message (`message`) about? Use `agent_previous_reply` to resolve what the message refers to. Each goal lists requests already filed under it."; goal.Instructions != want {
		t.Fatalf("instructions = %q", goal.Instructions)
	}
	want := map[string]string{
		"widget-G1": `{"goal":"search","existing_requests":["rank exact matches first"]}`,
		"widget-G2": `{"goal":"billing","existing_requests":[]}`,
		"new":       `"a new topic not covered by any goal above"`,
		"none":      `"pure thanks or acknowledgement with no topic"`,
	}
	if len(goal.Criteria) != len(want) {
		t.Fatalf("criteria = %v", goal.Criteria)
	}
	for id, text := range want {
		if string(goal.Criteria[id]) != text {
			t.Fatalf("criterion %s = %s, want %s", id, goal.Criteria[id], text)
		}
	}
}

func stub(t *testing.T, handler http.HandlerFunc) goalsort.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return goalsort.Client{Config: goalsort.Config{URL: server.URL + "/", Model: "d1-3b"}, HTTP: server.Client()}
}

func TestAskPostsToSystemOneAndReadsTheGoalAnswer(t *testing.T) {
	var method, path string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, `{"answers":{"goal":{"choice":"widget-G1","confidence":0.93}}}`)
	})
	answer, err := client.Ask(context.Background(), goalsort.Question{Message: "rank exact matches"})
	if err != nil || answer != (goalsort.Answer{Choice: "widget-G1", Confidence: 0.93}) {
		t.Fatalf("answer = %+v err=%v", answer, err)
	}
	if method != http.MethodPost || path != "/v1/systemone" {
		t.Fatalf("%s %s", method, path)
	}
}

func TestAnUnusableAnswerIsAnError(t *testing.T) {
	for name, reply := range map[string]func(http.ResponseWriter){
		"server error": func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
		"not json":     func(w http.ResponseWriter) { io.WriteString(w, "<html>") },
		"no choice":    func(w http.ResponseWriter) { io.WriteString(w, `{"answers":{}}`) },
		"bad score":    func(w http.ResponseWriter) { io.WriteString(w, `{"answers":{"goal":{"choice":"new","confidence":7}}}`) },
	} {
		client := stub(t, func(w http.ResponseWriter, r *http.Request) { reply(w) })
		if answer, err := client.Ask(context.Background(), goalsort.Question{Message: "x"}); err == nil {
			t.Fatalf("%s: answer %+v, want an error", name, answer)
		}
	}
}

func TestASlowSorterIsCutOffAtTheDeadline(t *testing.T) {
	release := make(chan struct{})
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := client.Ask(ctx, goalsort.Question{Message: "x"}); err == nil {
		t.Fatal("a sorter that never answered gave no error")
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("waited %s past the deadline", waited)
	}
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".agenthail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTheSorterIsOffUntilConfiguredAndRefusesAMalformedKey(t *testing.T) {
	home := t.TempDir()
	if _, ok, err := goalsort.LoadConfig(home); ok || err != nil {
		t.Fatalf("no config file: ok=%v err=%v", ok, err)
	}
	writeConfig(t, home, `{"other": true}`)
	if _, ok, err := goalsort.LoadConfig(home); ok || err != nil {
		t.Fatalf("no sorter key: ok=%v err=%v", ok, err)
	}
	writeConfig(t, home, `{"ledgerSorter": {"url": " http://127.0.0.1:8100 ", "model": "d1-3b"}}`)
	if config, ok, err := goalsort.LoadConfig(home); !ok || err != nil || config != (goalsort.Config{URL: "http://127.0.0.1:8100", Model: "d1-3b"}) {
		t.Fatalf("configured: %+v ok=%v err=%v", config, ok, err)
	}
	for _, bad := range []string{`{"ledgerSorter": "on"}`, `{"ledgerSorter": {"url": "127.0.0.1:8100", "model": "d1-3b"}}`, `{"ledgerSorter": {"url": "http://127.0.0.1:8100"}}`} {
		writeConfig(t, home, bad)
		if _, ok, err := goalsort.LoadConfig(home); ok || err == nil || !strings.Contains(err.Error(), "ledgerSorter must be") {
			t.Fatalf("%s: ok=%v err=%v", bad, ok, err)
		}
	}
}
