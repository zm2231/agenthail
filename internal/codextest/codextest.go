// Package codextest serves a fake Codex app-server daemon on the
// $CODEX_HOME control socket for tests. It keeps Codex's thread lifecycle:
// threads it creates or resumes are loaded and accept direct input, a turn
// can only start on a loaded thread, and every thread records its creator in
// originator. A test can answer any method itself with a Responder.
package codextest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Responder answers one JSON-RPC request. A nil result leaves the method to
// the fake's lifecycle; a result with an "error" key becomes a JSON-RPC
// error; Drop closes the connection without a response.
type Responder func(method string, params map[string]any) map[string]any

// Drop makes the fake close the connection instead of answering, the way a
// daemon that dies mid-request does.
var Drop = map[string]any{"drop": true}

type Call struct {
	Method string
	Params map[string]any
}

type Turn struct {
	ID        string
	Status    string
	User      string
	Assistant string
}

type Thread struct {
	ID         string
	Cwd        string
	Originator string
	Turns      []Turn
	// ActiveFlags are what an active thread reports it is blocked on, such as
	// waitingOnApproval.
	ActiveFlags []string
	// Source is the thread's app-server source; empty is "vscode".
	Source any
}

type Daemon struct {
	Home    string
	mu      sync.Mutex
	respond Responder
	calls   []Call
	pending []map[string]any
	threads map[string]*Thread
	order   []string
	loaded  map[string]bool
	next    int
	server  *http.Server
}

// Home creates an isolated CODEX_HOME and HOME. The control socket path must
// stay short, so it lives under /tmp.
func Home(t testing.TB) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ah-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	return home
}

func SocketPath(home string) string {
	return filepath.Join(home, "app-server-control", "app-server-control.sock")
}

func Start(t testing.TB, respond Responder) *Daemon {
	t.Helper()
	return Serve(t, Home(t), respond)
}

// Serve listens on home's control socket. It is safe to call from a
// goroutine, as a fake `codex app-server daemon start` does.
func Serve(t testing.TB, home string, respond Responder) *Daemon {
	if respond == nil {
		respond = func(string, map[string]any) map[string]any { return nil }
	}
	if err := os.MkdirAll(filepath.Dir(SocketPath(home)), 0700); err != nil {
		t.Error(err)
		return nil
	}
	_ = os.Remove(SocketPath(home))
	listener, err := net.Listen("unix", SocketPath(home))
	if err != nil {
		t.Error(err)
		return nil
	}
	daemon := &Daemon{Home: home, respond: respond, threads: map[string]*Thread{}, loaded: map[string]bool{}}
	upgrader := websocket.Upgrader{}
	daemon.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		daemon.serve(connection)
	})}
	go daemon.server.Serve(listener)
	t.Cleanup(daemon.Stop)
	return daemon
}

// Stop ends the daemon the way a crash does: connections close and every
// loaded thread is unloaded, while the socket file stays behind.
func (d *Daemon) Stop() {
	_ = d.server.Close()
	d.mu.Lock()
	d.loaded = map[string]bool{}
	d.mu.Unlock()
}

func (d *Daemon) serve(connection *websocket.Conn) {
	for {
		var request map[string]any
		if connection.ReadJSON(&request) != nil {
			return
		}
		id, hasID := request["id"]
		if !hasID {
			continue
		}
		method, _ := request["method"].(string)
		params, _ := request["params"].(map[string]any)
		d.mu.Lock()
		d.calls = append(d.calls, Call{Method: method, Params: params})
		d.mu.Unlock()
		result := d.respond(method, params)
		if result != nil && result["drop"] == true {
			return
		}
		result = d.lifecycle(method, params, result)
		var notifications []map[string]any
		if method == "thread/read" {
			d.mu.Lock()
			notifications, d.pending = d.pending, nil
			d.mu.Unlock()
		}
		for _, notification := range notifications {
			_ = connection.WriteJSON(notification)
		}
		response := map[string]any{"id": id, "result": result}
		if rpcError, failed := result["error"]; failed {
			response = map[string]any{"id": id, "error": rpcError}
		}
		_ = connection.WriteJSON(response)
	}
}

func (d *Daemon) Calls(method string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	var params []map[string]any
	for _, call := range d.calls {
		if call.Method == method {
			params = append(params, call.Params)
		}
	}
	return params
}

func (d *Daemon) Methods() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	methods := make([]string, 0, len(d.calls))
	for _, call := range d.calls {
		methods = append(methods, call.Method)
	}
	return methods
}

// Notify queues a notification ahead of the next thread/read response, the
// way the app-server interleaves them.
func (d *Daemon) Notify(method string, params map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = append(d.pending, map[string]any{"method": method, "params": params})
}

func (d *Daemon) AddThread(thread Thread) {
	d.mu.Lock()
	defer d.mu.Unlock()
	stored := thread
	d.threads[thread.ID] = &stored
	d.order = append(d.order, thread.ID)
	d.writeRollout(&stored)
}

// writeRollout records the thread's session_meta where Codex keeps rollouts,
// dated by the thread id's UUIDv7 timestamp.
func (d *Daemon) writeRollout(thread *Thread) {
	digits := strings.ReplaceAll(thread.ID, "-", "")
	if len(digits) < 12 || thread.Originator == "" {
		return
	}
	millis, err := strconv.ParseInt(digits[:12], 16, 64)
	if err != nil {
		return
	}
	created := time.UnixMilli(millis)
	path := filepath.Join(d.Home, "sessions", created.Format("2006"), created.Format("01"), created.Format("02"), "rollout-"+created.Format("2006-01-02T15-04-05")+"-"+thread.ID+".jsonl")
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": thread.ID, "originator": thread.Originator, "source": "vscode", "cwd": thread.Cwd}})
	if os.MkdirAll(filepath.Dir(path), 0700) == nil {
		_ = os.WriteFile(path, append(meta, '\n'), 0600)
	}
}

func (d *Daemon) Unload(threadID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.loaded, threadID)
}

func (d *Daemon) Loaded(threadID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loaded[threadID]
}

func (d *Daemon) CompleteTurn(threadID, reply string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if turn := d.activeTurn(threadID); turn != nil {
		turn.Status = "completed"
		turn.Assistant = reply
	}
}

// SetActiveFlags sets what the thread's active turn reports it is waiting on.
func (d *Daemon) SetActiveFlags(threadID string, flags ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if thread := d.threads[threadID]; thread != nil {
		thread.ActiveFlags = flags
	}
}

func (d *Daemon) Thread(threadID string) (Thread, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	thread, ok := d.threads[threadID]
	if !ok {
		return Thread{}, false
	}
	snapshot := *thread
	snapshot.Turns = append([]Turn(nil), thread.Turns...)
	return snapshot, true
}

func (d *Daemon) activeTurn(threadID string) *Turn {
	thread := d.threads[threadID]
	if thread == nil || len(thread.Turns) == 0 {
		return nil
	}
	turn := &thread.Turns[len(thread.Turns)-1]
	if turn.Status != "inProgress" {
		return nil
	}
	return turn
}

func rpcError(message string) map[string]any {
	return map[string]any{"error": map[string]any{"code": -32600, "message": message}}
}

func inputText(params map[string]any) string {
	input, _ := params["input"].([]any)
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if text, _ := item["text"].(string); text != "" {
			return text
		}
	}
	return ""
}

// lifecycle answers what the test left unanswered and fills in what a real
// daemon always reports about a thread the test described only partly.
func (d *Daemon) lifecycle(method string, params map[string]any, result map[string]any) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, failed := result["error"]; failed {
		return result
	}
	threadID, _ := params["threadId"].(string)
	if result != nil {
		d.fill(method, threadID, result)
		return result
	}
	switch method {
	case "thread/start":
		d.next++
		id := fmt.Sprintf("019f004a-0000-7000-8000-%012d", d.next)
		cwd, _ := params["cwd"].(string)
		d.threads[id] = &Thread{ID: id, Cwd: cwd, Originator: "agenthail"}
		d.order = append(d.order, id)
		d.writeRollout(d.threads[id])
		d.loaded[id] = true
		return map[string]any{"cwd": cwd, "thread": d.threadJSON(d.threads[id], false)}
	case "thread/resume":
		thread := d.threads[threadID]
		if thread == nil {
			return rpcError("thread not found: " + threadID)
		}
		d.loaded[threadID] = true
		return map[string]any{"thread": d.threadJSON(thread, false)}
	case "thread/read":
		thread := d.threads[threadID]
		if thread == nil {
			return rpcError("thread not found: " + threadID)
		}
		includeTurns, _ := params["includeTurns"].(bool)
		return map[string]any{"thread": d.threadJSON(thread, includeTurns)}
	case "thread/loaded/list":
		ids := make([]any, 0, len(d.loaded))
		for _, id := range d.order {
			if d.loaded[id] {
				ids = append(ids, id)
			}
		}
		return map[string]any{"data": ids}
	case "thread/list":
		threads := make([]any, 0, len(d.order))
		for index := len(d.order) - 1; index >= 0; index-- {
			threads = append(threads, d.threadJSON(d.threads[d.order[index]], false))
		}
		return map[string]any{"data": threads}
	case "thread/turns/list":
		thread := d.threads[threadID]
		if thread == nil {
			return rpcError("thread not found: " + threadID)
		}
		turns := make([]any, 0, len(thread.Turns))
		for index := len(thread.Turns) - 1; index >= 0; index-- {
			turns = append(turns, turnJSON(thread.Turns[index]))
		}
		return map[string]any{"data": turns}
	case "turn/start":
		thread := d.threads[threadID]
		if thread == nil || !d.loaded[threadID] {
			return rpcError("thread not found: " + threadID)
		}
		turn := Turn{ID: fmt.Sprintf("turn-%d", len(thread.Turns)+1), Status: "inProgress", User: inputText(params)}
		thread.Turns = append(thread.Turns, turn)
		return map[string]any{"turn": turnJSON(turn)}
	case "turn/steer":
		turn := d.activeTurn(threadID)
		expected, _ := params["expectedTurnId"].(string)
		if !d.loaded[threadID] || turn == nil || turn.ID != expected {
			return rpcError("no active turn to steer")
		}
		turn.User += "\n" + inputText(params)
		return map[string]any{"turnId": turn.ID}
	case "turn/interrupt":
		turn := d.activeTurn(threadID)
		if !d.loaded[threadID] || turn == nil {
			return rpcError("no active turn to interrupt")
		}
		turn.Status = "interrupted"
		return map[string]any{}
	case "model/list":
		return map[string]any{"data": []any{}}
	}
	return map[string]any{}
}

func (d *Daemon) fill(method, threadID string, result map[string]any) {
	switch method {
	case "thread/start", "thread/resume", "thread/fork":
		thread, _ := result["thread"].(map[string]any)
		if thread == nil {
			thread = map[string]any{}
			result["thread"] = thread
		}
		if method == "thread/resume" && thread["id"] == nil {
			thread["id"] = threadID
		}
		if id, _ := thread["id"].(string); id != "" {
			d.loaded[id] = true
		}
	case "thread/read":
		thread, _ := result["thread"].(map[string]any)
		if thread == nil {
			thread = map[string]any{}
			result["thread"] = thread
		}
		if thread["id"] == nil {
			thread["id"] = threadID
		}
		if _, present := thread["originator"]; !present {
			thread["originator"] = "agenthail"
		}
		if _, present := thread["canAcceptDirectInput"]; !present {
			thread["canAcceptDirectInput"] = d.loaded[threadID]
		}
	}
}

func (d *Daemon) threadJSON(thread *Thread, includeTurns bool) map[string]any {
	status := "notLoaded"
	if d.loaded[thread.ID] {
		status = "idle"
		if d.activeTurn(thread.ID) != nil {
			status = "active"
		}
	}
	preview := ""
	if len(thread.Turns) > 0 {
		preview = thread.Turns[0].User
	}
	threadStatus := map[string]any{"type": status}
	if status == "active" {
		flags := make([]any, 0, len(thread.ActiveFlags))
		for _, flag := range thread.ActiveFlags {
			flags = append(flags, flag)
		}
		threadStatus["activeFlags"] = flags
	}
	var source any = "vscode"
	if thread.Source != nil {
		source = thread.Source
	}
	value := map[string]any{
		"id":                   thread.ID,
		"cwd":                  thread.Cwd,
		"source":               source,
		"originator":           thread.Originator,
		"preview":              preview,
		"status":               threadStatus,
		"canAcceptDirectInput": d.loaded[thread.ID],
		"recencyAt":            float64(1792300000 + len(d.order)),
	}
	if thread.Originator == "" {
		delete(value, "originator")
	}
	if includeTurns {
		turns := make([]any, 0, len(thread.Turns))
		for _, turn := range thread.Turns {
			turns = append(turns, turnJSON(turn))
		}
		value["turns"] = turns
	}
	return value
}

func turnJSON(turn Turn) map[string]any {
	items := []any{map[string]any{"id": turn.ID + "-user", "type": "userMessage", "content": []any{map[string]any{"type": "text", "text": turn.User}}}}
	if turn.Assistant != "" {
		items = append(items, map[string]any{"id": turn.ID + "-reply", "type": "agentMessage", "phase": "final_answer", "text": turn.Assistant})
	}
	return map[string]any{"id": turn.ID, "status": turn.Status, "items": items}
}

func (d *Daemon) LoadedIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.loaded))
	for id := range d.loaded {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
