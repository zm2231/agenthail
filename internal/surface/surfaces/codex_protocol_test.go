package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

var nodeInspectorURL = regexp.MustCompile(`ws://127\.0\.0\.1:(\d+)/`)

func startNodeDesktopBridge(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the Desktop bridge integration test")
	}
	dir := t.TempDir()
	child := dir + "/app-server.js"
	main := dir + "/desktop-main.js"
	if err := os.WriteFile(child, []byte(`let buffer='';process.stdin.on('data',chunk=>{buffer+=chunk;let i;while((i=buffer.indexOf('\n'))>=0){const line=buffer.slice(0,i);buffer=buffer.slice(i+1);if(!line)continue;const request=JSON.parse(line);if(request.params&&request.params.mode==='timeout')continue;if(request.params&&request.params.mode==='malformed')process.stdout.write('{bad json}\n');process.stdout.write(JSON.stringify({jsonrpc:'2.0',method:'turn/progress',params:{threadId:'thread'}})+'\n');process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:request.id,result:{method:request.method,params:request.params}})+'\n')}});`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte(`const {spawn}=require('child_process');spawn(process.execPath,[process.argv[2]],{stdio:['pipe','pipe','inherit']});setInterval(()=>{},1000);`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--inspect=127.0.0.1:0", main, child)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("Node inspector exited before publishing its endpoint")
			}
			matches := nodeInspectorURL.FindStringSubmatch(line)
			if len(matches) == 2 {
				return "http://127.0.0.1:" + matches[1]
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for Node inspector")
		}
	}
}

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

type paginatedThreadClient struct {
	methods []string
	params  []map[string]any
}

func (c *paginatedThreadClient) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	c.methods = append(c.methods, method)
	c.params = append(c.params, params)
	switch method {
	case "thread/read":
		return map[string]any{"result": map[string]any{"thread": map[string]any{"id": "thread", "status": map[string]any{"type": "idle"}, "turns": []any{}}}}, nil
	case "thread/turns/list":
		return map[string]any{"result": map[string]any{"data": []any{map[string]any{"id": "turn", "status": map[string]any{"type": "completed"}}}}}, nil
	case "thread/items/list":
		return map[string]any{"result": map[string]any{"data": []any{
			map[string]any{"item": map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "question"}}}},
			map[string]any{"item": map[string]any{"type": "agentMessage", "text": "answer"}},
		}}}, nil
	default:
		return nil, fmt.Errorf("unexpected method %s", method)
	}
}

type observationThreadClient struct {
	methods    []string
	turnParams map[string]any
	itemCalls  int
}

func (c *observationThreadClient) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	c.methods = append(c.methods, method)
	switch method {
	case "thread/read":
		return map[string]any{"result": map[string]any{"thread": map[string]any{"id": "thread", "status": map[string]any{"type": "busy"}, "turns": []any{}}}}, nil
	case "thread/turns/list":
		c.turnParams = params
		turns := make([]any, 50)
		turns[0] = map[string]any{"id": "active", "status": map[string]any{"type": "inProgress"}}
		for index := 1; index < len(turns); index++ {
			turns[index] = map[string]any{"id": fmt.Sprintf("old-%d", index), "status": map[string]any{"type": "completed"}}
		}
		return map[string]any{"result": map[string]any{"data": turns}}, nil
	case "thread/items/list":
		c.itemCalls++
		turnID, _ := params["turnId"].(string)
		if turnID == "active" {
			return map[string]any{"result": map[string]any{"data": []any{map[string]any{"item": map[string]any{"type": "userMessage", "text": "working"}}}}}, nil
		}
		return map[string]any{"result": map[string]any{"data": []any{map[string]any{"item": map[string]any{"type": "agentMessage", "text": "latest answer"}}}}}, nil
	default:
		return nil, fmt.Errorf("unexpected method %s", method)
	}
}

func (c *observationThreadClient) Close() error { return nil }

func TestCodexObservationHydratesOnlyLatestCandidates(t *testing.T) {
	client := &observationThreadClient{}
	thread, err := NewCodex("").readObservationThread(context.Background(), client, "thread")
	if err != nil {
		t.Fatal(err)
	}
	observation := codexObservation(thread)
	if observation.Status != surface.StatusBusy || observation.ActiveTurnID != "active" || observation.Reply == nil || observation.Reply.Text != "latest answer" {
		t.Fatalf("observation=%+v", observation)
	}
	if client.itemCalls != 2 {
		t.Fatalf("item calls=%d, want 2", client.itemCalls)
	}
	if page, _ := client.turnParams["page"].(map[string]any); page["limit"] != 3 {
		t.Fatalf("turn params=%v", client.turnParams)
	}
}

func TestCodexActiveTurnUsesBoundedReader(t *testing.T) {
	client := &observationThreadClient{}
	active, err := NewCodex("").activeTurnID(context.Background(), client, "thread")
	if err != nil || active != "active" || client.itemCalls != 2 {
		t.Fatalf("active=%q item_calls=%d err=%v", active, client.itemCalls, err)
	}
	if page, _ := client.turnParams["page"].(map[string]any); page["limit"] != 3 {
		t.Fatalf("turn params=%v", client.turnParams)
	}
}

func TestCodexDesktopBridgeFramesChildRPC(t *testing.T) {
	codex := NewCodex(startNodeDesktopBridge(t))
	client, err := codex.openDesktop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Request(context.Background(), "turn/start", map[string]any{"threadId": "thread", "text": strings.Repeat("x", 4096)}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := response["result"].(map[string]any)
	if result["method"] != "turn/start" {
		t.Fatalf("response=%v", response)
	}
	desktop := client.(*desktopCodexClient)
	eventsValue, err := desktop.conn.evaluate(context.Background(), codexEventsJS(0), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var events codexEventBatch
	if err := json.Unmarshal([]byte(eventsValue.(string)), &events); err != nil {
		t.Fatal(err)
	}
	if len(events.Events) != 1 || events.Events[0].Method != "turn/progress" {
		t.Fatalf("events=%+v", events)
	}
	_, err = client.Request(context.Background(), "turn/start", map[string]any{"mode": "malformed"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Request(context.Background(), "turn/start", map[string]any{"mode": "timeout"}, 30*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error=%v", err)
	}
}

func (c *paginatedThreadClient) Close() error { return nil }

func TestCodexReadThreadHydratesPaginatedDesktopTurns(t *testing.T) {
	client := &paginatedThreadClient{}
	thread, err := NewCodex("").readThread(context.Background(), client, "thread")
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Turns) != 1 || thread.Turns[0].User != "question" || thread.Turns[0].Assistant != "answer" || !thread.Turns[0].Done {
		t.Fatalf("thread=%+v", thread)
	}
	if got := strings.Join(client.methods, ","); got != "thread/read,thread/turns/list,thread/items/list" {
		t.Fatalf("methods=%s", got)
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

func TestResolveCodexNodeEndpointUsesMainProcessTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"type": "page", "webSocketDebuggerUrl": "ws://ignored"},
			{"type": "node", "webSocketDebuggerUrl": "ws://main"},
		})
	}))
	defer server.Close()
	targets, err := resolveCodexNodeEndpoint(context.Background(), server.URL)
	if err != nil || len(targets) != 1 || targets[0].wsURL != "ws://main" {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
}

func TestCodexResolveExactNameUsesHistorySearch(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	searchCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
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
			case strings.Contains(expression, "process._getActiveHandles"):
				value = "already"
			case strings.Contains(expression, `"thread/search"`):
				searchCalls++
				value = `{"result":{"data":[{"thread":{"id":"thread-1","name":"Q","cwd":"/tmp","source":"vscode","status":{"type":"idle"}},"snippet":"test"}]}}`
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	session, err := NewCodex(server.URL).Resolve(context.Background(), "Q")
	if err != nil || session == nil || session.ID != "thread-1" || session.Transport != codexTransportDesktop || searchCalls != 1 {
		t.Fatalf("session=%+v search_calls=%d err=%v", session, searchCalls, err)
	}
}

func TestCodexResolveIDReadsThreadWithoutListing(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	readCalls := 0
	listCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
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
			value := any("")
			switch {
			case strings.Contains(expression, "process._getActiveHandles"):
				value = "already"
			case strings.Contains(expression, `"thread/loaded/list"`):
				value = `{"result":{"data":[]}}`
			case strings.Contains(expression, `"thread/read"`):
				readCalls++
				value = `{"result":{"thread":{"id":"019f004a-a94e-7313-a599-2db587a1f67a","name":"known","cwd":"/tmp","source":"vscode","status":{"type":"idle"}}}}`
			case strings.Contains(expression, `"thread/list"`):
				listCalls++
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	session, err := NewCodex(server.URL).Resolve(context.Background(), "019f004a-a94e-7313-a599-2db587a1f67a")
	if err != nil || session == nil || session.ID != "019f004a-a94e-7313-a599-2db587a1f67a" || session.Transport != codexTransportDesktop || readCalls != 1 || listCalls != 0 {
		t.Fatalf("session=%+v read_calls=%d list_calls=%d err=%v", session, readCalls, listCalls, err)
	}
}

func TestCodexResolveRejectsDuplicateExactNamesAcrossPages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	searchCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
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
			case strings.Contains(expression, "process._getActiveHandles"):
				value = "already"
			case strings.Contains(expression, `"thread/search"`):
				searchCalls++
				value = `{"result":{"data":[{"thread":{"id":"thread-1","name":"duplicate","cwd":"/one"}},{"thread":{"id":"thread-2","name":"DUPLICATE","cwd":"/two"}}]}}`
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

func TestCodexListUsesOneBoundedStateDatabasePage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	listCalls := 0
	loadedCalls := 0
	readCalls := 0
	listExpression := ""
	loadedExpression := ""
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
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
			case strings.Contains(expression, "process._getActiveHandles"):
				value = "already"
			case strings.Contains(expression, `"thread/loaded/list"`):
				loadedCalls++
				loadedExpression = expression
				value = `{"result":{"data":["loaded-thread"]}}`
			case strings.Contains(expression, `"thread/read"`):
				readCalls++
				value = `{"result":{"thread":{"id":"loaded-thread","name":"loaded session","status":"busy"}}}`
			case strings.Contains(expression, `"thread/list"`):
				listCalls++
				listExpression = expression
				value = fmt.Sprintf(`{"result":{"data":[{"id":"thread-%d","name":"session"}],"nextCursor":"cursor-%d"}}`, listCalls, listCalls)
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	sessions, err := NewCodex(server.URL).List(context.Background())
	if err != nil || len(sessions) != 2 || listCalls != 1 || loadedCalls != 1 || readCalls != 1 {
		t.Fatalf("sessions=%v list_calls=%d loaded_calls=%d read_calls=%d err=%v", sessions, listCalls, loadedCalls, readCalls, err)
	}
	for _, required := range []string{`"page":{"limit":50}`} {
		if !strings.Contains(listExpression, required) {
			t.Fatalf("bounded list expression missing %s: %s", required, listExpression)
		}
	}
	for _, unsupported := range []string{`"useStateDbOnly"`, `"sortKey"`, `"sortDirection"`} {
		if strings.Contains(listExpression, unsupported) {
			t.Fatalf("desktop list expression contains unsupported %s: %s", unsupported, listExpression)
		}
	}
	if !strings.Contains(loadedExpression, `"limit":50`) {
		t.Fatalf("loaded list expression missing limit: %s", loadedExpression)
	}
}

func TestCodexReadyUsesLoadedListOnly(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	loadedCalls := 0
	listCalls := 0
	readCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
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
			value := any("")
			switch {
			case strings.Contains(expression, "process._getActiveHandles"):
				value = "already"
			case strings.Contains(expression, `"thread/loaded/list"`):
				loadedCalls++
				value = `{"result":{"data":[]}}`
			case strings.Contains(expression, `"thread/list"`):
				listCalls++
			case strings.Contains(expression, `"thread/read"`):
				readCalls++
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	if err := NewCodex(server.URL).Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loadedCalls != 1 || listCalls != 0 || readCalls != 0 {
		t.Fatalf("loaded_calls=%d list_calls=%d read_calls=%d", loadedCalls, listCalls, readCalls)
	}
}

func TestCodexBridgeUsesDesktopAppServerChildStdio(t *testing.T) {
	for name, source := range map[string]string{
		"hook":   codexHookJS,
		"rpc":    codexRPCJSONJS("thread/list", `{}`, time.Second),
		"staged": codexStagedRPCJS("payload", "turn/start", time.Second),
	} {
		if !strings.Contains(source, "__agenthailDesktopAppServerV1") {
			t.Fatalf("%s does not use the Desktop app-server bridge", name)
		}
	}
	for _, required := range []string{"process._getActiveHandles", "handle.stdin.write", "handle.stdout.on", "spawnargs"} {
		if !strings.Contains(codexHookJS, required) {
			t.Fatalf("Desktop app-server bridge missing %q", required)
		}
	}
}

func TestCodexBridgeUsesDedicatedRequestIDs(t *testing.T) {
	if !strings.Contains(codexHookJS, "next: 900000") {
		t.Fatal("Desktop app-server bridge does not isolate request IDs")
	}
}

func TestCodexDesktopDiscoveryRetriesImmediatelyForReplacementTarget(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var mu sync.Mutex
	target := "one"
	hookCalls := map[string]int{}
	compatible := map[string]bool{"one": true, "three": true, "four": true}
	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		current := target
		mu.Unlock()
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/" + current}})
	})
	for _, name := range []string{"one", "two", "three", "four"} {
		name := name
		handler.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
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
				mu.Lock()
				hookCalls[name]++
				mu.Unlock()
				value := any("no-app-server-child")
				if compatible[name] {
					value = "hooked"
				}
				_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
			}
		})
	}
	server = httptest.NewServer(handler)
	defer server.Close()
	codex := NewCodex(server.URL)
	client, err := codex.openDesktop(context.Background())
	if err != nil {
		t.Fatalf("initial target did not connect: %v", err)
	}
	client.Close()
	mu.Lock()
	target = "two"
	mu.Unlock()
	if client, err := codex.openDesktop(context.Background()); err == nil {
		client.Close()
		t.Fatal("replacement without a dispatcher connected")
	}
	mu.Lock()
	firstCalls := hookCalls["two"]
	mu.Unlock()
	if client, err := codex.openDesktop(context.Background()); err == nil {
		client.Close()
		t.Fatal("unsupported replacement target reconnected")
	}
	mu.Lock()
	secondCalls := hookCalls["two"]
	target = "three"
	mu.Unlock()
	if secondCalls != firstCalls {
		t.Fatalf("same target repeated expensive discovery: first=%d second=%d", firstCalls, secondCalls)
	}
	client, err = codex.openDesktop(context.Background())
	if err != nil {
		t.Fatalf("replacement target did not rebind: %v", err)
	}
	client.Close()
	mu.Lock()
	target = "four"
	mu.Unlock()
	client, err = codex.openDesktop(context.Background())
	if err != nil {
		t.Fatalf("second replacement target did not rebind: %v", err)
	}
	client.Close()
	mu.Lock()
	thirdCalls := hookCalls["three"]
	fourthCalls := hookCalls["four"]
	mu.Unlock()
	if thirdCalls == 0 || fourthCalls == 0 {
		t.Fatalf("replacement dispatchers were not rediscovered: three=%d four=%d", thirdCalls, fourthCalls)
	}
}

func TestCodexDesktopDiscoveryRetriesSameTargetAfterBackoff(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var mu sync.Mutex
	ready := false
	hookCalls := 0
	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/desktop"}})
	})
	handler.HandleFunc("/desktop", func(w http.ResponseWriter, r *http.Request) {
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
			mu.Lock()
			hookCalls++
			value := any("no-app-server-child")
			if ready {
				value = "hooked"
			}
			mu.Unlock()
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()
	codex := NewCodex(server.URL)
	if client, err := codex.openDesktop(context.Background()); err == nil {
		client.Close()
		t.Fatal("unready Desktop bridge connected")
	}
	mu.Lock()
	firstCalls := hookCalls
	mu.Unlock()
	if client, err := codex.openDesktop(context.Background()); err == nil {
		client.Close()
		t.Fatal("cached unready Desktop bridge connected")
	}
	mu.Lock()
	secondCalls := hookCalls
	ready = true
	mu.Unlock()
	if secondCalls != firstCalls {
		t.Fatalf("same target repeated discovery before backoff: first=%d second=%d", firstCalls, secondCalls)
	}
	codex.bridgeMu.Lock()
	codex.bridgeRetry = time.Now().Add(-time.Millisecond)
	codex.bridgeMu.Unlock()
	client, err := codex.openDesktop(context.Background())
	if err != nil {
		t.Fatalf("same target did not recover after backoff: %v", err)
	}
	client.Close()
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
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
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
			case strings.Contains(expression, "process._getActiveHandles"):
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
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
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
			case strings.Contains(expression, "process._getActiveHandles"):
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
