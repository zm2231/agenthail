package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

func signedIn(account map[string]any) codextest.Responder {
	return func(method string, _ map[string]any) map[string]any {
		if method == "account/read" {
			return map[string]any{"account": account, "requiresOpenaiAuth": true}
		}
		return nil
	}
}

var chatgptAccount = map[string]any{"type": "chatgpt", "planType": "pro", "email": nil}

func managedVoice(t *testing.T, respond codextest.Responder) (*codextest.Daemon, *CodexVoice, *surface.Session) {
	t.Helper()
	daemon := startManagedCodex(t, respond)
	v := &CodexVoice{Codex: isolatedManagedRuntime(t)}
	session, err := v.Create(context.Background(), "/tmp/voice-operator", "instructions")
	if err != nil {
		t.Fatal(err)
	}
	return daemon, v, session
}

func pollUntil(t *testing.T, v *CodexVoice, cursor voice.Cursor, threadID string, want int) voice.Batch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var collected voice.Batch
	for {
		batch, err := v.Poll(context.Background(), cursor, threadID, 0)
		if err != nil {
			t.Fatal(err)
		}
		collected.Events = append(collected.Events, batch.Events...)
		collected.Lost = collected.Lost || batch.Lost
		collected.Cursor, cursor = batch.Cursor, batch.Cursor
		if len(collected.Events) >= want || time.Now().After(deadline) {
			return collected
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func responseTo(t *testing.T, daemon *codextest.Daemon, id any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, response := range daemon.Responses() {
			if response["id"] == id {
				return response
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no response to %v: %v", id, daemon.Responses())
	return nil
}

func TestANewVoiceOperatorIsCreatedOnTheAppServerWithItsToolsAndName(t *testing.T) {
	daemon, _, session := managedVoice(t, nil)
	if session.Transport != codexTransportManaged || session.RuntimeKind != surface.RuntimeCodexAppServer || session.Name != voiceOperatorName {
		t.Fatalf("session=%+v", session)
	}
	starts := daemon.Calls("thread/start")
	if len(starts) != 1 || starts[0]["dynamicTools"] == nil {
		t.Fatalf("thread/start=%v", starts)
	}
	names := daemon.Calls("thread/name/set")
	if len(names) != 1 || names[0]["threadId"] != session.ID || names[0]["name"] != voiceOperatorName {
		t.Fatalf("thread/name/set=%v", names)
	}
}

func TestAManagedCallCarriesItsEventsAndAnswersToolCallsWithTheirOriginalIDs(t *testing.T) {
	daemon, v, session := managedVoice(t, signedIn(chatgptAccount))
	ctx := context.Background()
	cursor, err := v.Cursor(ctx, session)
	if err != nil || !strings.HasPrefix(cursor.Source, "managed:") {
		t.Fatalf("cursor=%+v err=%v", cursor, err)
	}
	if resumes := daemon.Calls("thread/resume"); len(resumes) != 1 || resumes[0]["threadId"] != session.ID {
		t.Fatalf("thread/resume=%v", resumes)
	}
	if err := v.Request(ctx, session, "thread/realtime/start", voice.StartParams(session.ID, "call-a", "v=0 offer", false)); err != nil {
		t.Fatal(err)
	}
	if starts := daemon.Calls("thread/realtime/start"); len(starts) != 1 {
		t.Fatalf("thread/realtime/start=%v", starts)
	}
	daemon.Notify("thread/realtime/sdp", map[string]any{"threadId": session.ID, "sdp": "v=0 answer"})
	daemon.Notify("thread/realtime/outputAudio/delta", map[string]any{"threadId": session.ID, "audio": "excluded"})
	daemon.Notify("thread/realtime/sdp", map[string]any{"threadId": "another-thread", "sdp": "excluded"})
	daemon.Request(7, "item/tool/call", map[string]any{"threadId": session.ID, "namespace": "agenthail", "tool": "voice_projects", "arguments": map[string]any{}, "callId": "c1", "turnId": "t1"})
	daemon.Request("named-request", "item/tool/call", map[string]any{"threadId": session.ID, "namespace": "agenthail", "tool": "voice_projects", "arguments": map[string]any{}, "callId": "c2", "turnId": "t1"})

	batch := pollUntil(t, v, cursor, session.ID, 3)
	var methods, requests []string
	for _, event := range batch.Events {
		methods = append(methods, event.Method)
		if id, _ := event.Params["requestId"].(string); id != "" {
			requests = append(requests, id)
		}
	}
	if strings.Join(methods, " ") != "thread/realtime/sdp item/tool/call item/tool/call" || batch.Lost {
		t.Fatalf("events=%v lost=%v", methods, batch.Lost)
	}
	if err := v.RespondDynamicToolCall(ctx, requests[0], "another-thread", true, "[]"); err == nil {
		t.Fatal("answered a tool call for another thread")
	}
	if err := v.RespondDynamicToolCall(ctx, requests[0], session.ID, true, "projects"); err != nil {
		t.Fatal(err)
	}
	if err := v.RespondDynamicToolCall(ctx, requests[1], session.ID, false, "refused"); err != nil {
		t.Fatal(err)
	}
	numeric := responseTo(t, daemon, float64(7))
	result, _ := numeric["result"].(map[string]any)
	items, _ := result["contentItems"].([]any)
	if result["success"] != true || len(items) != 1 || items[0].(map[string]any)["text"] != "projects" {
		t.Fatalf("response=%v", numeric)
	}
	named := responseTo(t, daemon, "named-request")
	if result, _ := named["result"].(map[string]any); result["success"] != false {
		t.Fatalf("response=%v", named)
	}
	if err := v.RespondDynamicToolCall(ctx, requests[0], session.ID, true, "again"); err == nil {
		t.Fatal("answered one tool call twice")
	}
	again := pollUntil(t, v, cursor, session.ID, 3)
	for _, event := range again.Events {
		if event.Method == "item/tool/call" {
			t.Fatalf("an answered tool call was offered again: %+v", event)
		}
	}
}

func TestEachManagedCallHasItsOwnConnection(t *testing.T) {
	daemon, v, session := managedVoice(t, signedIn(chatgptAccount))
	ctx := context.Background()
	first, err := v.Cursor(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	second, err := v.Cursor(ctx, session)
	if err != nil || second.Source == first.Source {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
	daemon.Notify("thread/realtime/sdp", map[string]any{"threadId": session.ID, "sdp": "v=0 answer"})
	if batch := pollUntil(t, v, first, session.ID, 1); !batch.Lost {
		t.Fatalf("an earlier call's cursor read the new call's events: %+v", batch)
	}
	if batch := pollUntil(t, v, second, session.ID, 1); batch.Lost || len(batch.Events) != 1 {
		t.Fatalf("batch=%+v", batch)
	}
}

func TestAManagedCallStartRefusalSaysWhatFixesTheLogin(t *testing.T) {
	for _, test := range []struct {
		name    string
		account map[string]any
		refuse  bool
		want    []string
		started int
	}{
		{name: "signed out", want: []string{"not signed in", "codex login"}},
		{name: "login unknown and refused", refuse: true, want: []string{"401 Unauthorized", "codex login"}, started: 1},
		{name: "api key refused", account: map[string]any{"type": "apiKey"}, refuse: true, want: []string{"401 Unauthorized", "OpenAI Realtime API", "codex login"}, started: 1},
		{name: "chatgpt refused", account: chatgptAccount, refuse: true, want: []string{"401 Unauthorized", "ChatGPT's realtime service", "codex login"}, started: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon, v, session := managedVoice(t, func(method string, _ map[string]any) map[string]any {
				switch {
				case method == "account/read" && test.name == "login unknown and refused":
					return map[string]any{"error": map[string]any{"code": -32601, "message": "method not found"}}
				case method == "account/read":
					return map[string]any{"account": test.account, "requiresOpenaiAuth": true}
				case method == "thread/realtime/start" && test.refuse:
					return map[string]any{"error": map[string]any{"code": -32000, "message": "401 Unauthorized"}}
				}
				return nil
			})
			if _, err := v.Cursor(context.Background(), session); err != nil {
				t.Fatal(err)
			}
			err := v.Request(context.Background(), session, "thread/realtime/start", voice.StartParams(session.ID, "call-a", "v=0 offer", false))
			var rejected *voice.RejectedError
			if !errors.As(err, &rejected) {
				t.Fatalf("err=%v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(rejected.Reason, want) {
					t.Fatalf("reason %q lacks %q", rejected.Reason, want)
				}
			}
			if got := len(daemon.Calls("thread/realtime/start")); got != test.started {
				t.Fatalf("thread/realtime/start calls=%d", got)
			}
		})
	}
}

func TestAManagedPollReturnsAToolCallAsSoonAsCodexSendsIt(t *testing.T) {
	daemon, v, session := managedVoice(t, signedIn(chatgptAccount))
	cursor, err := v.Cursor(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	type polled struct {
		batch voice.Batch
		err   error
		at    time.Time
	}
	done := make(chan polled, 1)
	go func() {
		batch, err := v.Poll(context.Background(), cursor, session.ID, 5*time.Second)
		done <- polled{batch, err, time.Now()}
	}()
	select {
	case got := <-done:
		t.Fatalf("the poll returned before Codex sent anything: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
	sent := time.Now()
	var after voice.Cursor
	daemon.Request(7, "item/tool/call", map[string]any{"threadId": session.ID, "namespace": "agenthail", "tool": "voice_projects", "arguments": map[string]any{}, "callId": "c1", "turnId": "t1"})
	select {
	case got := <-done:
		if got.err != nil || len(got.batch.Events) != 1 || got.batch.Events[0].Method != "item/tool/call" {
			t.Fatalf("batch=%+v err=%v", got.batch, got.err)
		}
		if elapsed := got.at.Sub(sent); elapsed >= 100*time.Millisecond {
			t.Fatalf("the tool call took %v to reach the poll", elapsed)
		}
		after = got.batch.Cursor
	case <-time.After(5 * time.Second):
		t.Fatal("the poll waited out its bound with a tool call pending")
	}
	started := time.Now()
	batch, err := v.Poll(context.Background(), after, session.ID, 30*time.Millisecond)
	if err != nil || len(batch.Events) != 0 || batch.Lost || time.Since(started) < 30*time.Millisecond {
		t.Fatalf("a quiet poll returned %+v err=%v after %v", batch, err, time.Since(started))
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := v.Poll(ctx, batch.Cursor, session.ID, 5*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled poll returned %v", err)
	}
}

func TestADroppedManagedCallConnectionReportsLostEvents(t *testing.T) {
	_, v, session := managedVoice(t, signedIn(chatgptAccount))
	cursor, err := v.Cursor(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	_ = v.link(session.ID).client.ws.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		batch, err := v.Poll(context.Background(), cursor, session.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if batch.Lost {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a dropped connection never reported its events lost")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if v.link(session.ID) != nil {
		t.Fatal("the dropped connection stayed the call's link")
	}
	if batch, err := v.Poll(context.Background(), cursor, session.ID, 0); err != nil || !batch.Lost {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
}

func TestAVoiceOperatorUsesDesktopOnlyWhileDesktopHasItLoaded(t *testing.T) {
	const operator = "019f004a-0000-7000-8000-0000000000aa"
	var loaded atomic.Value
	loaded.Store(`{"result":{"data":[]}}`)
	desktop := codextest.StartDesktop(t, func(method string) string {
		if method == "thread/loaded/list" {
			return loaded.Load().(string)
		}
		return `{"result":{}}`
	})
	startManagedCodex(t, nil)
	codex := NewCodex(desktop.URL)
	codex.managed = true
	v := &CodexVoice{Codex: codex}
	session := &surface.Session{ID: operator, Surface: surface.KindCodex}
	if route, err := v.voiceRoute(context.Background(), session); err != nil || route != codexTransportManaged {
		t.Fatalf("route=%q err=%v", route, err)
	}
	loaded.Store(`{"result":{"data":["` + operator + `"]}}`)
	if route, err := v.voiceRoute(context.Background(), session); err != nil || route != codexTransportDesktop {
		t.Fatalf("route=%q err=%v", route, err)
	}
}

func TestAVoiceCallRunsEndToEndOnTheAppServerWithoutDesktop(t *testing.T) {
	daemon := startManagedCodex(t, signedIn(chatgptAccount))
	service := voice.New(t.TempDir()+"/voice/operator.json", &CodexVoice{Codex: isolatedManagedRuntime(t)}, nil, "/fixture/agenthail")
	t.Cleanup(service.Close)
	ctx := context.Background()
	if _, err := service.Apply(ctx, "phone", voice.Action{Action: "prepare"}); err != nil {
		t.Fatal(err)
	}
	operator := service.View("phone").Session
	if operator == nil || operator.Transport != codexTransportManaged {
		t.Fatalf("operator=%+v", operator)
	}
	if _, err := service.Apply(ctx, "phone", voice.Action{Action: "start", AttemptID: "call-a", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"}); err != nil {
		t.Fatal(err)
	}
	daemon.Notify("thread/realtime/started", map[string]any{"threadId": operator.ID, "realtimeSessionId": "call-a"})
	daemon.Notify("thread/realtime/sdp", map[string]any{"threadId": operator.ID, "sdp": "v=0 answer"})
	deadline := time.Now().Add(10 * time.Second)
	for service.View("phone").SDP != "v=0 answer" {
		if time.Now().After(deadline) {
			t.Fatalf("the call never negotiated: %+v", service.View("phone"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	daemon.Request(5, "item/tool/call", map[string]any{"threadId": operator.ID, "namespace": "agenthail", "tool": "voice_return_to_orchestrator", "arguments": map[string]any{}, "callId": "c1", "turnId": "t1"})
	answer := responseTo(t, daemon, float64(5))
	if result, _ := answer["result"].(map[string]any); result["success"] != true {
		t.Fatalf("answer=%v", answer)
	}
	if methods := strings.Join(daemon.Methods(), " "); !strings.Contains(methods, "thread/realtime/start") {
		t.Fatalf("methods=%s", methods)
	}
}

func TestARefusedOperatorNameIsSetWhenTheNextCallOpens(t *testing.T) {
	var refusals atomic.Int32
	refusals.Store(1)
	daemon, v, session := managedVoice(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/name/set" && refusals.Add(-1) >= 0 {
			return map[string]any{"error": map[string]any{"code": -32000, "message": "busy"}}
		}
		return signedIn(chatgptAccount)(method, nil)
	})
	if thread, _ := daemon.Thread(session.ID); thread.Name != "" {
		t.Fatalf("thread=%+v", thread)
	}
	if _, err := v.Cursor(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if thread, _ := daemon.Thread(session.ID); thread.Name != voiceOperatorName {
		t.Fatalf("the next call did not name the operator: %+v", thread)
	}
	if _, err := v.Cursor(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if names := daemon.Calls("thread/name/set"); len(names) != 2 {
		t.Fatalf("a named operator was named again: %v", names)
	}
}

func TestAnOperatorThatCannotBeNamedRefusesTheCall(t *testing.T) {
	_, v, session := managedVoice(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/name/set" {
			return map[string]any{"error": map[string]any{"code": -32000, "message": "busy"}}
		}
		return nil
	})
	if _, err := v.Cursor(context.Background(), session); err == nil || !strings.Contains(err.Error(), "name the voice operator") {
		t.Fatalf("err=%v", err)
	}
	if v.link(session.ID) != nil {
		t.Fatal("a refused call kept a link")
	}
}

func TestAReleasedCallClosesItsConnection(t *testing.T) {
	_, v, session := managedVoice(t, signedIn(chatgptAccount))
	cursor, err := v.Cursor(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	link := v.link(session.ID)
	v.Release(session.ID, voice.Cursor{Source: "managed:an-earlier-call"})
	if v.link(session.ID) != link {
		t.Fatal("releasing an earlier call closed the current call's link")
	}
	v.Release(session.ID, cursor)
	if v.link(session.ID) != nil {
		t.Fatal("a released call kept its link")
	}
	select {
	case <-link.followed:
	case <-time.After(5 * time.Second):
		t.Fatal("a released call kept reading its connection")
	}
}

func TestASignedOutCallAndAServiceShutdownEachCloseTheCallsConnection(t *testing.T) {
	for _, test := range []struct {
		name    string
		account map[string]any
	}{{name: "signed out"}, {name: "shutdown", account: chatgptAccount}} {
		t.Run(test.name, func(t *testing.T) {
			daemon := startManagedCodex(t, signedIn(test.account))
			provider := &CodexVoice{Codex: isolatedManagedRuntime(t)}
			service := voice.New(t.TempDir()+"/voice/operator.json", provider, nil, "/fixture/agenthail")
			t.Cleanup(service.Close)
			ctx := context.Background()
			if _, err := service.Apply(ctx, "phone", voice.Action{Action: "prepare"}); err != nil {
				t.Fatal(err)
			}
			_, err := service.Apply(ctx, "phone", voice.Action{Action: "start", AttemptID: "call-a", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"})
			if (test.account == nil) != (err != nil) {
				t.Fatalf("err=%v", err)
			}
			if test.account != nil {
				if daemon.Connections() != 1 {
					t.Fatalf("a started call has %d connections", daemon.Connections())
				}
				service.Close()
			}
			deadline := time.Now().Add(5 * time.Second)
			for daemon.Connections() != 0 {
				if time.Now().After(deadline) {
					t.Fatalf("the call kept %d connections open", daemon.Connections())
				}
				time.Sleep(10 * time.Millisecond)
			}
			provider.mu.Lock()
			links := len(provider.links)
			provider.mu.Unlock()
			if links != 0 {
				t.Fatalf("the call kept %d links", links)
			}
		})
	}
}

func TestAStartWhoseStateCannotBeSavedClosesTheCallsConnection(t *testing.T) {
	daemon := startManagedCodex(t, signedIn(chatgptAccount))
	path := t.TempDir() + "/voice/operator.json"
	service := voice.New(path, &CodexVoice{Codex: isolatedManagedRuntime(t)}, nil, "/fixture/agenthail")
	t.Cleanup(service.Close)
	ctx := context.Background()
	if _, err := service.Apply(ctx, "phone", voice.Action{Action: "prepare"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	if _, err := service.Apply(ctx, "phone", voice.Action{Action: "start", AttemptID: "call-a", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"}); err == nil {
		t.Fatal("a start with unpersistable state was accepted")
	}
	deadline := time.Now().Add(5 * time.Second)
	for daemon.Connections() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the call kept %d connections open", daemon.Connections())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestASpokenRequestOnASessionBoundCallReachesOnlyTheSession(t *testing.T) {
	daemon := startManagedCodex(t, signedIn(chatgptAccount))
	codex := isolatedManagedRuntime(t)
	const targetID = "019f004a-0000-7000-8000-00000000cafe"
	daemon.AddThread(codextest.Thread{ID: targetID, Cwd: "/tmp/target", Originator: "agenthail", Source: "agenthail"})
	resolve := func(ctx context.Context, id string) (*voice.Target, error) {
		session, err := codex.Resolve(ctx, id)
		if err != nil {
			return nil, err
		}
		return &voice.Target{Session: session, Adapter: codex}, nil
	}
	service := voice.NewWithTargets(t.TempDir()+"/voice/operator.json", &CodexVoice{Codex: codex}, nil, "/fixture/agenthail", resolve, delivery.Dispatcher{})
	t.Cleanup(service.Close)
	ctx := context.Background()
	for _, action := range []voice.Action{{Action: "prepare"}, {Action: "select", TargetID: targetID}, {Action: "start", AttemptID: "call-a", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"}} {
		if _, err := service.Apply(ctx, "phone", action); err != nil {
			t.Fatalf("%s: %v", action.Action, err)
		}
	}
	operator := service.View("phone").Session
	starts := daemon.Calls("thread/realtime/start")
	if len(starts) != 1 || starts[0]["prompt"] == nil || starts[0]["includeStartupContext"] != false {
		t.Fatalf("the session-bound call kept Codex's delegating prompt: %v", starts)
	}
	daemon.Notify("thread/realtime/started", map[string]any{"threadId": operator.ID, "realtimeSessionId": "call-a"})
	daemon.Notify("thread/realtime/sdp", map[string]any{"threadId": operator.ID, "sdp": "v=0 answer"})
	deadline := time.Now().Add(10 * time.Second)
	for service.View("phone").Phase != "negotiating" {
		if time.Now().After(deadline) {
			t.Fatalf("the call never negotiated: %+v", service.View("phone"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := service.Apply(ctx, "phone", voice.Action{Action: "connected", AttemptID: "call-a"}); err != nil {
		t.Fatal(err)
	}
	daemon.Notify("thread/realtime/transcript/done", map[string]any{"threadId": operator.ID, "realtimeSessionId": "call-a", "role": "user", "text": "Run the tests"})
	deadline = time.Now().Add(10 * time.Second)
	for len(daemon.Calls("turn/start")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the spoken request never reached the session: %s", strings.Join(daemon.Methods(), " "))
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, turn := range daemon.Calls("turn/start") {
		if turn["threadId"] != targetID {
			t.Fatalf("a spoken request started a turn on %v, not the session", turn["threadId"])
		}
	}
	for _, steer := range daemon.Calls("turn/steer") {
		if steer["threadId"] == operator.ID {
			t.Fatalf("a spoken request steered the operator: %v", steer)
		}
	}
}
