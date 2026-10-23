package daemon

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

// testPushBrowser is a browser's side of Web Push: it holds the keys a
// subscription names and decrypts what the push service passes on.
type testPushBrowser struct {
	key  *ecdh.PrivateKey
	auth []byte
}

func newTestPushBrowser(t *testing.T) testPushBrowser {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return testPushBrowser{key: key, auth: auth}
}

func (b testPushBrowser) subscription(deviceID, endpoint string) registry.BrowserPushSubscription {
	return registry.BrowserPushSubscription{
		DeviceID: deviceID,
		Endpoint: endpoint,
		P256DH:   base64.RawURLEncoding.EncodeToString(b.key.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(b.auth),
	}
}

// decrypt opens an aes128gcm body as RFC 8291 describes.
func (b testPushBrowser) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body is %d bytes", len(body))
	}
	salt, keyLength := body[:16], int(body[20])
	senderKey, err := ecdh.P256().NewPublicKey(body[21 : 21+keyLength])
	if err != nil {
		t.Fatal(err)
	}
	shared, err := b.key.ECDH(senderKey)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), b.key.PublicKey().Bytes()...), senderKey.Bytes()...)
	ikm, err := hkdf.Key(sha256.New, shared, b.auth, string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	contentKey, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(contentKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := gcm.Open(nil, nonce, body[21+keyLength:], nil)
	if err != nil {
		t.Fatal(err)
	}
	end := bytes.LastIndexByte(plain, 0x02)
	if end < 0 {
		t.Fatal("no padding delimiter")
	}
	return plain[:end]
}

type receivedPush struct {
	header  http.Header
	message webPushMessage
}

type testPushService struct {
	server *httptest.Server
	mu     sync.Mutex
	status int
	pushes []receivedPush
}

func newTestPushService(t *testing.T, browser testPushBrowser) *testPushService {
	t.Helper()
	service := &testPushService{status: http.StatusCreated}
	service.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var message webPushMessage
		if err := json.Unmarshal(browser.decrypt(t, body), &message); err != nil {
			t.Errorf("payload: %v", err)
		}
		service.mu.Lock()
		service.pushes = append(service.pushes, receivedPush{header: r.Header.Clone(), message: message})
		status := service.status
		service.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(service.server.Close)
	previous := webPushHTTPClient
	webPushHTTPClient = service.server.Client()
	t.Cleanup(func() { webPushHTTPClient = previous })
	return service
}

func (s *testPushService) received() []receivedPush {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]receivedPush(nil), s.pushes...)
}

func subscribedTestBrowser(t *testing.T, endpoint string, browser testPushBrowser) (*registry.Registry, registry.Device) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	reg := registryTestRegistry(t)
	pairing, err := reg.CreateDevicePairing("Browser", registry.BrowserScopes, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := reg.CompleteBrowserPairing(pairing.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SaveBrowserPushSubscription(browser.subscription(device.ID, endpoint)); err != nil {
		t.Fatal(err)
	}
	return reg, device
}

func TestBrowsersGetEncryptedNotificationsAndWithdrawals(t *testing.T) {
	browser := newTestPushBrowser(t)
	service := newTestPushService(t, browser)
	reg, _ := subscribedTestBrowser(t, service.server.URL+"/push/secret-capability", browser)
	d := newTestDaemon(t, reg, nil)
	key := waitingNotificationKey("session", "question")
	d.notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Claude asks you a question", SessionID: "session", TurnID: "question", EventType: "session.waiting", CollapseID: key, RequestID: "request"})
	d.withdrawFromPairedDevices(context.Background(), key)
	pushes := service.received()
	if len(pushes) != 2 {
		t.Fatalf("pushes=%+v", pushes)
	}
	shown, withdrawn := pushes[0], pushes[1]
	if want := (webPushMessage{Type: "notify", Title: "Agenthail", Message: "Claude asks you a question", SessionID: "session", TurnID: "question", EventType: "session.waiting", CollapseID: key, RequestID: "request"}); shown.message != want {
		t.Fatalf("shown=%+v", shown.message)
	}
	if want := (webPushMessage{Type: "withdraw", CollapseID: key}); withdrawn.message != want {
		t.Fatalf("withdrawn=%+v", withdrawn.message)
	}
	topic := shown.header.Get("Topic")
	if topic == "" || len(topic) > 32 || withdrawn.header.Get("Topic") != topic {
		t.Fatalf("topics %q and %q", topic, withdrawn.header.Get("Topic"))
	}
	if shown.header.Get("Urgency") != "high" || withdrawn.header.Get("Urgency") != "normal" || shown.header.Get("TTL") != "900" {
		t.Fatalf("headers=%v", shown.header)
	}
	keys, err := webPushKey()
	if err != nil {
		t.Fatal(err)
	}
	if authorization := shown.header.Get("Authorization"); !strings.HasPrefix(authorization, "vapid t=") || !strings.HasSuffix(authorization, ", k="+keys.PublicKey) {
		t.Fatalf("authorization=%q", authorization)
	}
	info, err := os.Stat(WebPushKeyPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode=%v err=%v", info.Mode(), err)
	}
}

func TestABrowserThatCannotCloseGetsNotificationsButNoWithdraws(t *testing.T) {
	browser := newTestPushBrowser(t)
	service := newTestPushService(t, browser)
	endpoint := service.server.URL + "/push/secret-capability"
	reg, device := subscribedTestBrowser(t, endpoint, browser)
	if err := reg.StopBrowserPushWithdraws(device.ID, endpoint); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, reg, nil)
	key := waitingNotificationKey("session", "question")
	d.notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Claude asks you a question", SessionID: "session", TurnID: "question", EventType: "session.waiting", CollapseID: key})
	d.withdrawFromPairedDevices(context.Background(), key)
	pushes := service.received()
	if len(pushes) != 1 || pushes[0].message.Type != "notify" {
		t.Fatalf("pushes=%+v", pushes)
	}
}

func TestOnlyAGoneSubscriptionIsRemovedAndItsEndpointIsNeverLogged(t *testing.T) {
	for _, test := range []struct {
		status int
		kept   bool
	}{
		{status: http.StatusGone, kept: false},
		{status: http.StatusNotFound, kept: false},
		{status: http.StatusTooManyRequests, kept: true},
		{status: http.StatusInternalServerError, kept: true},
		{status: http.StatusForbidden, kept: true},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			browser := newTestPushBrowser(t)
			service := newTestPushService(t, browser)
			service.status = test.status
			endpoint := service.server.URL + "/push/secret-capability"
			reg, device := subscribedTestBrowser(t, endpoint, browser)
			d := newTestDaemon(t, reg, nil)
			var logged bytes.Buffer
			d.log = log.New(&logged, "", 0)
			d.notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Finished", EventType: "turn.completed"})
			if found, err := reg.HasBrowserPushSubscription(device.ID); err != nil || found != test.kept {
				t.Fatalf("subscription kept=%v err=%v, want %v", found, err, test.kept)
			}
			if !strings.Contains(logged.String(), "push service returned") || strings.Contains(logged.String(), "secret-capability") || strings.Contains(logged.String(), service.server.URL) {
				t.Fatalf("log=%q", logged.String())
			}
		})
	}
}

func TestAnUnreachablePushServiceIsLoggedWithoutItsEndpoint(t *testing.T) {
	browser := newTestPushBrowser(t)
	service := newTestPushService(t, browser)
	endpoint := service.server.URL + "/push/secret-capability"
	reg, device := subscribedTestBrowser(t, endpoint, browser)
	service.server.Close()
	d := newTestDaemon(t, reg, nil)
	var logged bytes.Buffer
	d.log = log.New(&logged, "", 0)
	d.notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Finished", EventType: "turn.completed"})
	if !strings.Contains(logged.String(), "browser notification "+device.ID) || strings.Contains(logged.String(), "secret-capability") || strings.Contains(logged.String(), service.server.URL) {
		t.Fatalf("log=%q", logged.String())
	}
	if found, err := reg.HasBrowserPushSubscription(device.ID); err != nil || !found {
		t.Fatalf("an unreachable push service removed the subscription: %v %v", found, err)
	}
}
