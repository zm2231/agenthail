package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/cfaccess"
	"github.com/zm2231/agenthail/internal/cfaccess/cfaccesstest"
)

type pushListener struct {
	session *mcp.ClientSession
	updated chan string
	logged  chan map[string]any
}

func (f *remoteFixture) listen(t *testing.T, headers map[string]string) *pushListener {
	t.Helper()
	l := &pushListener{updated: make(chan string, 16), logged: make(chan map[string]any, 16)}
	l.session = f.connect(t, headers, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) { l.updated <- req.Params.URI },
		LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) {
			encoded, _ := json.Marshal(req.Params.Data)
			var data map[string]any
			_ = json.Unmarshal(encoded, &data)
			l.logged <- data
		},
	})
	if err := l.session.SetLoggingLevel(t.Context(), &mcp.SetLoggingLevelParams{Level: "notice"}); err != nil {
		t.Fatal(err)
	}
	if err := l.session.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "agenthail://inbox/scout"}); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *pushListener) expect(t *testing.T, what string, unread float64, secret string) {
	t.Helper()
	select {
	case uri := <-l.updated:
		if uri != "agenthail://inbox/scout" {
			t.Fatalf("%s: updated %s", what, uri)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: no resources/updated", what)
	}
	select {
	case data := <-l.logged:
		encoded, _ := json.Marshal(data)
		if data["unread"] != unread || data["inbox"] != "agenthail://inbox/scout" || strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "builder") {
			t.Fatalf("%s: notice %s", what, encoded)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: no notice", what)
	}
}

func (f *remoteFixture) notices(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { f.d.runRemoteInboxNotices(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func (f *remoteFixture) tracked() int {
	m := f.d.remoteMCP()
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func TestARemoteIdentityIsPushedAContentlessNoticeAndToldAgainOnReconnect(t *testing.T) {
	for _, mode := range []string{"token only", "access and token"} {
		t.Run(mode, func(t *testing.T) {
			f := newRemoteFixture(t, "client-a")
			headers := map[string]string{"Authorization": "Bearer " + f.token}
			if mode == "access and token" {
				f.access(t)
				headers[cfaccess.Header] = f.issuer.Sign(t, cfaccesstest.Token("client-a"))
			}
			f.notices(t)
			listener := f.listen(t, headers)
			f.deliver(t, "first secret")
			listener.expect(t, "a new message", 1, "secret")
			read, refused := callMCP(t, listener.session, "agenthail_read", nil)
			if messages, _ := read["messages"].([]any); refused || len(messages) != 1 {
				t.Fatalf("read=%v refused=%v", read, refused)
			}
			listener.session.Close()

			f.deliver(t, "second secret")
			listener = f.listen(t, headers)
			listener.expect(t, "unread on reconnect", 2, "secret")
		})
	}
}

func TestACredentialChangeClosesEveryRemoteStreamBeforeAnotherNotice(t *testing.T) {
	changes := map[string]func(*remoteFixture) error{
		"rotate":          func(f *remoteFixture) error { _, _, err := f.r.RotateRemoteIdentity("scout"); return err },
		"off":             func(f *remoteFixture) error { return f.r.UnbindRemoteIdentity("scout") },
		"remove identity": func(f *remoteFixture) error { return f.r.RemoveAgentIdentity("scout") },
		"access cleared and restored": func(f *remoteFixture) error {
			if err := f.r.ClearAccessSettings(); err != nil {
				return err
			}
			return f.r.SetAccessSettings(cfaccesstest.Team, cfaccesstest.AUD)
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			f := newRemoteFixture(t, "client-a")
			headers := map[string]string{"Authorization": "Bearer " + f.token}
			if name == "access cleared and restored" {
				f.access(t)
				headers[cfaccess.Header] = f.issuer.Sign(t, cfaccesstest.Token("client-a"))
			}
			f.notices(t)
			listener := f.listen(t, headers)
			for deadline := time.Now().Add(5 * time.Second); f.tracked() != 1; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the listener was never bound")
				}
			}
			if err := change(f); err != nil {
				t.Fatal(err)
			}
			if name != "remove identity" {
				f.deliver(t, "after the change")
			}
			ended := make(chan struct{})
			go func() { _ = listener.session.Wait(); close(ended) }()
			select {
			case <-ended:
			case <-time.After(10 * time.Second):
				t.Fatal("the stream stayed open after the credential changed")
			}
			select {
			case uri := <-listener.updated:
				t.Fatalf("a closed stream was told of %s", uri)
			case data := <-listener.logged:
				t.Fatalf("a closed stream got %v", data)
			default:
			}
		})
	}
}

func TestARevocationRacingANoticeLeaksNoContentAndClosesTheStream(t *testing.T) {
	f := newRemoteFixture(t, "")
	m := f.d.remoteMCP()
	var once sync.Once
	m.beforeNotice = func() {
		once.Do(func() {
			if _, _, err := f.r.RotateRemoteIdentity("scout"); err != nil {
				t.Error(err)
			}
		})
	}
	f.notices(t)
	listener := f.listen(t, map[string]string{"Authorization": "Bearer " + f.token})
	f.deliver(t, "the secret text")
	ended := make(chan struct{})
	go func() { _ = listener.session.Wait(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream stayed open after the racing rotation")
	}
	for {
		select {
		case data := <-listener.logged:
			if encoded, _ := json.Marshal(data); strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "builder") {
				t.Fatalf("the racing notice carried content: %s", encoded)
			}
			continue
		default:
		}
		break
	}
}

func TestASessionStaysBoundUntilItEndsWhateverTheNotifierIsDoing(t *testing.T) {
	f := newRemoteFixture(t, "")
	m := f.d.remoteMCP()
	stop := make(chan struct{})
	passes := make(chan struct{})
	go func() {
		defer close(passes)
		for {
			select {
			case <-stop:
				return
			default:
				m.notify(t.Context())
			}
		}
	}()
	headers := map[string]string{"Authorization": "Bearer " + f.token}
	first := f.listen(t, headers)
	second := f.listen(t, headers)
	time.Sleep(200 * time.Millisecond)
	if got := f.tracked(); got != 2 {
		close(stop)
		<-passes
		t.Fatalf("tracked=%d while the notifier ran", got)
	}
	first.session.Close()
	for deadline := time.Now().Add(5 * time.Second); f.tracked() != 1; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			close(stop)
			<-passes
			t.Fatalf("tracked=%d after one session ended", f.tracked())
		}
	}
	close(stop)
	<-passes
	f.deliver(t, "still there")
	m.notify(t.Context())
	second.expect(t, "the remaining session", 1, "still there")
}

func TestAnAbandonedRemoteSessionIsReclaimed(t *testing.T) {
	f := newRemoteFixture(t, "", func(d *Daemon) { d.remoteIdle = 300 * time.Millisecond })
	transport := &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: headerTransport{"Authorization": "Bearer " + f.token}}, MaxRetries: -1, DisableStandaloneSSE: true}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "gone", Version: "1"}, nil).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	callMCP(t, session, "agenthail_whoami", nil)
	if f.tracked() != 1 {
		t.Fatalf("tracked=%d after a call", f.tracked())
	}
	for deadline := time.Now().Add(10 * time.Second); f.tracked() != 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("an abandoned session was never reclaimed")
		}
	}
}

func TestTheInboxResourceSaysOnlyHowManyAreUnread(t *testing.T) {
	f := newRemoteFixture(t, "")
	f.deliver(t, "private words")
	session := f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil)
	resource, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "agenthail://inbox/scout"})
	if err != nil || len(resource.Contents) != 1 || strings.Contains(resource.Contents[0].Text, "private") || !strings.Contains(resource.Contents[0].Text, `"unread":1`) {
		t.Fatalf("resource=%+v err=%v", resource, err)
	}
	if _, err := f.r.AddAgentIdentity("builder"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "agenthail://inbox/builder"}); err == nil {
		t.Fatal("read another identity's inbox resource")
	}
	if err := session.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "agenthail://inbox/builder"}); err == nil {
		t.Fatal("subscribed to another identity's inbox")
	}
}

func TestASubscribeDuringANoticePassIsToldAgain(t *testing.T) {
	f := newRemoteFixture(t, "")
	m := f.d.remoteMCP()
	listener := f.listen(t, map[string]string{"Authorization": "Bearer " + f.token})
	var once sync.Once
	m.beforeNotice = func() {
		once.Do(func() {
			if err := listener.session.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "agenthail://inbox/scout"}); err != nil {
				t.Error(err)
			}
		})
	}
	f.deliver(t, "waiting")
	f.notices(t)
	listener.expect(t, "the pass the subscribe interrupted", 1, "waiting")
	listener.expect(t, "the retell the subscribe asked for", 1, "waiting")
}

func TestASessionVerifiedBeforeACredentialChangeIsNeverBound(t *testing.T) {
	f := newRemoteFixture(t, "")
	m := f.d.remoteMCP()
	other, err := f.r.AddAgentIdentity("builder")
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	m.afterEpoch = func() {
		for session := range m.server.Sessions() {
			if session.InitializeParams() != nil {
				once.Do(func() {
					if _, _, err := f.r.BindRemoteIdentity(other.Name, "", ""); err != nil {
						t.Error(err)
					}
				})
			}
		}
	}
	transport := &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: headerTransport{"Authorization": "Bearer " + f.token}}, MaxRetries: -1, DisableStandaloneSSE: true}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "late", Version: "1"}, nil).Connect(t.Context(), transport, nil)
	if err == nil {
		defer session.Close()
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		live := 0
		for range m.server.Sessions() {
			live++
		}
		if live == 0 && f.tracked() == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("live=%d tracked=%d: a session verified before the change stayed open", live, f.tracked())
		}
	}
}

func TestStoppingTheNotifierClosesEveryRemoteSession(t *testing.T) {
	f := newRemoteFixture(t, "")
	m := f.d.remoteMCP()
	if status, _ := postMCP(t, f.server, map[string]string{"Authorization": "Bearer " + f.token}); status != http.StatusOK {
		t.Fatalf("initialize status=%d", status)
	}
	f.listen(t, map[string]string{"Authorization": "Bearer " + f.token})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { f.d.runRemoteInboxNotices(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the notifier did not stop")
	}
	for range m.server.Sessions() {
		t.Fatal("a remote session outlived the notifier")
	}
}
