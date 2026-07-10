package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexObservationUsesTurnIDsAndCompletion(t *testing.T) {
	thread := &codexThread{Status: surface.StatusUnknown, Turns: []codexTurn{
		{ID: "done", Status: surface.StatusIdle, User: "one", Assistant: "same", Done: true},
		{ID: "running", Status: surface.StatusBusy, User: "two", Assistant: "partial"},
	}}
	observation := codexObservation(thread)
	if observation.Status != surface.StatusBusy || observation.ActiveTurnID != "running" || observation.CompletedTurnID != "done" || observation.Reply.Text != "same" {
		t.Fatalf("observation=%+v", observation)
	}
}

func TestCodexObservationSkipsEmptyTerminalTurnForReply(t *testing.T) {
	thread := &codexThread{Status: surface.StatusIdle, Turns: []codexTurn{
		{ID: "answer", Status: surface.StatusIdle, Assistant: "actual reply", Done: true},
		{ID: "empty", Status: surface.StatusIdle, Done: true},
	}}
	observation := codexObservation(thread)
	if observation.TerminalTurnID != "empty" || observation.CompletedTurnID != "answer" || observation.Reply == nil || observation.Reply.Text != "actual reply" {
		t.Fatalf("observation=%+v", observation)
	}
}

func TestCodexFailedTurnIsCompletedWithExplicitError(t *testing.T) {
	status, done, message := codexTurnState(map[string]any{"type": "failed"})
	if status != surface.StatusIdle || !done || message != "turn failed" {
		t.Fatalf("status=%s done=%v message=%q", status, done, message)
	}
	observation := codexObservation(&codexThread{Turns: []codexTurn{{ID: "failed", Status: status, Done: done, Assistant: "partial", Error: message}}})
	if observation.Reply == nil || observation.Reply.Error != "turn failed" {
		t.Fatalf("observation=%+v", observation)
	}
}

func TestCodexSystemErrorTurnIsTerminal(t *testing.T) {
	status, done, message := codexTurnState(map[string]any{"type": "systemError"})
	if status != surface.StatusIdle || !done || message != "turn systemerror" {
		t.Fatalf("status=%s done=%v message=%q", status, done, message)
	}
}

func TestCodexEventCorrelationHelpers(t *testing.T) {
	params := map[string]any{"thread": map[string]any{"id": "thread-1"}, "turn": map[string]any{"id": "turn-1"}, "delta": "hello"}
	if !codexContainsID(params, "thread-1") || codexContainsID(params, "thread-2") {
		t.Fatal("correlation mismatch")
	}
	if text := codexEventText(params); text != "hello" {
		t.Fatalf("text=%q", text)
	}
	if !codexCompletionMethod("turn/completed") || codexCompletionMethod("item/started") {
		t.Fatal("completion classification")
	}
}

func TestResolveCodexEndpointPrioritizesPrimaryRenderer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"type": "page", "url": "https://example.com", "webSocketDebuggerUrl": "ws://ignored"},
			{"type": "page", "url": "app://-/hotkey.html", "webSocketDebuggerUrl": "ws://hotkey"},
			{"type": "page", "url": "app://-/index.html", "webSocketDebuggerUrl": "ws://primary"},
		})
	}))
	defer server.Close()
	targets, err := resolveCodexEndpoint(context.Background(), server.URL, true)
	if err != nil || len(targets) != 2 || targets[0].wsURL != "ws://primary" || targets[1].wsURL != "ws://hotkey" {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
}

func TestResolveCodexEndpointRejectsUnrelatedTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"type": "page", "url": "https://example.com", "webSocketDebuggerUrl": "ws://unrelated"},
			{"type": "service_worker", "url": "app://-/worker.js", "webSocketDebuggerUrl": "ws://worker"},
		})
	}))
	defer server.Close()
	if targets, err := resolveCodexEndpoint(context.Background(), server.URL, true); err == nil || len(targets) != 0 || !strings.Contains(err.Error(), "no Codex renderer") {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
}

func TestCodexResolveExactNameScansForAmbiguity(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	listCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "page", "url": "app://-/index.html", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
	})
	handler.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			var value any = ""
			switch {
			case expression == codexRendererCapabilityJS:
				value = true
			case strings.Contains(expression, "typeof current.handler === 'function'"):
				value = "already"
			case strings.Contains(expression, `"thread/list"`):
				listCalls++
				if listCalls == 1 {
					value = `{"result":{"data":[{"id":"thread-1","name":"test-session-23","cwd":"/tmp","status":{"type":"idle"}}],"nextCursor":"more"}}`
				} else {
					value = `{"result":{"data":[{"id":"thread-2","name":"another-session","cwd":"/tmp","status":{"type":"idle"}}]}}`
				}
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	session, err := NewCodex(server.URL).Resolve(context.Background(), "test-session-23")
	if err != nil || session == nil || session.ID != "thread-1" || listCalls != 2 {
		t.Fatalf("session=%+v list_calls=%d err=%v", session, listCalls, err)
	}
}

func TestCodexResolveRejectsDuplicateExactNamesAcrossPages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	listCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "page", "url": "app://-/index.html", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
	})
	handler.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			var value any = ""
			switch {
			case expression == codexRendererCapabilityJS:
				value = true
			case strings.Contains(expression, "typeof current.handler === 'function'"):
				value = "already"
			case strings.Contains(expression, `"thread/list"`):
				listCalls++
				if listCalls == 1 {
					value = `{"result":{"data":[{"id":"thread-1","name":"duplicate","cwd":"/one"}],"nextCursor":"more"}}`
				} else {
					value = `{"result":{"data":[{"id":"thread-2","name":"DUPLICATE","cwd":"/two"}]}}`
				}
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	_, err := NewCodex(server.URL).Resolve(context.Background(), "duplicate")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "thread-1") || !strings.Contains(err.Error(), "thread-2") {
		t.Fatalf("err=%v", err)
	}
}

func TestCodexListReportsPaginationLimitInsteadOfReturningPartialResults(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	listCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "page", "url": "app://-/index.html", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
	})
	handler.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			var value any = ""
			switch {
			case expression == codexRendererCapabilityJS:
				value = true
			case strings.Contains(expression, "typeof current.handler === 'function'"):
				value = "already"
			case strings.Contains(expression, `"thread/list"`):
				listCalls++
				value = fmt.Sprintf(`{"result":{"data":[{"id":"thread-%d","name":"session"}],"nextCursor":"cursor-%d"}}`, listCalls, listCalls)
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	sessions, err := NewCodex(server.URL).List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pagination limit of 100 pages") || sessions != nil || listCalls != maxCodexListPages {
		t.Fatalf("sessions=%v list_calls=%d err=%v", sessions, listCalls, err)
	}
}

func TestCodexBridgeNeverTouchesAppServerChildStdio(t *testing.T) {
	for name, source := range map[string]string{
		"hook":       codexHookJS,
		"rpc":        codexRPCJSONJS("thread/list", `{}`, time.Second),
		"staged":     codexStagedRPCJS("payload", "turn/start", time.Second),
		"trampoline": codexRendererTrampolineJS("true"),
	} {
		for _, forbidden := range []string{"_getActiveHandles", ".stdin.write", ".stdout.on", "spawnargs"} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s contains forbidden raw-child access %q", name, forbidden)
			}
		}
	}
	if !strings.Contains(codexHookJS, "send-cli-request-for-host") || !strings.Contains(codexHookJS, "seen.size < 400") {
		t.Fatal("renderer dispatcher discovery or bound is missing")
	}
}

func TestCodexTurnCorrelationRequiresThreadAndRequestedTurn(t *testing.T) {
	event := map[string]any{"threadId": "thread-1", "turnId": "old-turn"}
	if !codexContainsID(event, "thread-1") {
		t.Fatal("thread correlation missing")
	}
	if codexContainsID(event, "requested-turn") {
		t.Fatal("stale turn correlated")
	}
}

func TestDiagnosticExcerptBoundsUnicodeErrorBodies(t *testing.T) {
	body := strings.Repeat("界", 2*1024*1024)
	excerpt := diagnosticExcerpt(body)
	if len(excerpt) > maxDiagnosticBytes+100 || !strings.Contains(excerpt, "truncated") || !json.Valid([]byte(strconvQuote(excerpt))) {
		t.Fatalf("excerpt bytes=%d", len(excerpt))
	}
}

func TestCodexRPCPropagatesErrorEnvelope(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if err := conn.ReadJSON(&request); err != nil {
				return
			}
			id := request["id"]
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			value := ""
			switch {
			case strings.Contains(expression, "await b.request"):
				value = `{"jsonrpc":"2.0","id":"agenthail-test","error":{"code":-32601,"message":"missing"}}`
			}
			response := map[string]any{"id": id, "result": map[string]any{"result": map[string]any{"value": value}}}
			if err := conn.WriteJSON(response); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	bridge := &cdpConn{ws: conn, next: 1}
	_, err = (&Codex{}).rpc(context.Background(), bridge, "missing/method", map[string]any{}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "RPC error") {
		t.Fatalf("err=%v", err)
	}
}

func TestCodexRPCRejectsMalformedDesktopResponse(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if conn.ReadJSON(&request) != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": "not-json"}}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = (&Codex{}).rpc(context.Background(), &cdpConn{ws: conn, next: 1}, "thread/list", map[string]any{}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "parse thread/list response") {
		t.Fatalf("err=%v", err)
	}
}

func TestCodexEventBatchJSONShape(t *testing.T) {
	raw, _ := json.Marshal(codexEventBatch{Cursor: 2, Events: []codexEvent{{Sequence: 2, Method: "turn/completed"}}})
	var decoded codexEventBatch
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Cursor != 2 {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func TestCodexRPCStagesLargePayloadInBoundedExpressions(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var longestExpression int
	var sawStaging bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			if len(expression) > longestExpression {
				longestExpression = len(expression)
			}
			value := ""
			switch {
			case strings.Contains(expression, "__agenthailPayloads") && !strings.Contains(expression, "delete p"):
				sawStaging = true
				value = "ok"
			case strings.Contains(expression, "TextDecoder"):
				value = `{"jsonrpc":"2.0","id":"agenthail-test","result":{}}`
			}
			response := map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}}
			if conn.WriteJSON(response) != nil {
				return
			}
		}
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	bridge := &cdpConn{ws: conn, next: 1}
	// The fake response lookup is keyed by an unpredictable UUID, so return a valid
	// envelope independent of its id; rpc only requires a result envelope here.
	_, err = (&Codex{}).rpc(context.Background(), bridge, "turn/start", map[string]any{"text": strings.Repeat("界", 100_000)}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !sawStaging || longestExpression > 2600 {
		t.Fatalf("staging=%v longest_expression=%d", sawStaging, longestExpression)
	}
}

func TestCodexStreamIgnoresStaleCompletionFromSameThread(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	eventReads := 0
	threadReads := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "page", "url": "app://-/index.html", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
	})
	handler.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			var value any = ""
			switch {
			case expression == codexRendererCapabilityJS:
				value = true
			case strings.Contains(expression, "typeof current.handler === 'function'"):
				value = "already"
			case expression == codexEventCursorJS:
				value = float64(10)
			case strings.Contains(expression, "events:b.events.filter"):
				eventReads++
				turnID := "old-turn"
				if eventReads > 1 {
					turnID = "target-turn"
				}
				batch, _ := json.Marshal(codexEventBatch{Cursor: int64(10 + eventReads), Events: []codexEvent{{Sequence: int64(10 + eventReads), Method: "turn/completed", Params: map[string]any{"threadId": "thread-1", "turnId": turnID}}}})
				value = string(batch)
			case strings.Contains(expression, `"thread/read"`):
				threadReads++
				status := "running"
				if threadReads > 1 {
					status = "completed"
				}
				value = fmt.Sprintf(`{"jsonrpc":"2.0","result":{"thread":{"id":"thread-1","status":{"type":"idle"},"turns":[{"id":"target-turn","status":{"type":"%s"},"items":[{"type":"agentMessage","text":"done"}]}]}}}`, status)
			}
			response := map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}}
			if conn.WriteJSON(response) != nil {
				return
			}
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	var events []surface.StreamEvent
	err := NewCodex(server.URL).Stream(context.Background(), &surface.Session{ID: "thread-1"}, "target-turn", func(event surface.StreamEvent) {
		events = append(events, event)
	}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if eventReads < 2 || len(events) != 2 || events[0].Text != "done" || events[1].Kind != "done" {
		t.Fatalf("event_reads=%d events=%+v", eventReads, events)
	}
}

func TestCodexStreamRecoversCompletionThatPredatesCursorSnapshot(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "page", "url": "app://-/index.html", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
	})
	handler.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			var value any = ""
			switch {
			case expression == codexRendererCapabilityJS:
				value = true
			case strings.Contains(expression, "typeof current.handler === 'function'"):
				value = "already"
			case strings.Contains(expression, `"thread/read"`):
				value = `{"jsonrpc":"2.0","result":{"thread":{"id":"thread-1","status":{"type":"idle"},"turns":[{"id":"target-turn","status":{"type":"completed"},"items":[{"type":"agentMessage","text":"fast"}]}]}}}`
			}
			conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()
	var events []surface.StreamEvent
	err := NewCodex(server.URL).Stream(context.Background(), &surface.Session{ID: "thread-1"}, "target-turn", func(event surface.StreamEvent) { events = append(events, event) }, time.Second)
	if err != nil || len(events) != 2 || events[0].Text != "fast" || events[1].Kind != "done" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}
