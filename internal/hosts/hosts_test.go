package hosts

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestQualifyAndSplit(t *testing.T) {
	if got := Qualify("h0123456789ab", "019a-thread"); got != "h0123456789ab::019a-thread" {
		t.Fatalf("qualify=%q", got)
	}
	if got := Qualify("h0123456789ab", "hffffffffffff::x"); got != "hffffffffffff::x" {
		t.Fatalf("double qualify=%q", got)
	}
	if got := Qualify("", "x"); got != "x" {
		t.Fatalf("local qualify=%q", got)
	}
	host, target, ok := Split("forge::@builder/reviewer")
	if !ok || host != "forge" || target != "@builder/reviewer" {
		t.Fatalf("split=%q %q %v", host, target, ok)
	}
	if _, target, ok := Split("codex:019a"); ok || target != "codex:019a" {
		t.Fatalf("surface selector split as host: %q %v", target, ok)
	}
	if _, _, ok := Split("::x"); ok {
		t.Fatal("empty host accepted")
	}
}

func TestQualifyJSONRewritesOnlySessionReferences(t *testing.T) {
	raw := []byte(`{"ok":true,"result":{"sessionId":"s1","turnId":"t1","queueId":4},"session":{"id":"s1","surface":"codex","subagent":{"parentId":"p1","rootId":"p1"}},"attachment":{"id":"a1"},"items":[{"sessionId":"s2"}],"runs":[{"jobId":"j1","sessionId":"s3","resumeSessionId":"s4"}],"subagents":[{"parentSessionId":"s5","agentId":"agent-1","toolUseId":"tool-1"}],"relay":{"fromSession":"s6","toSession":"s7"},"count":12345678901234567890}`)
	got, err := QualifyJSON("h0123456789ab", raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{`"sessionId":"h0123456789ab::s1"`, `"id":"h0123456789ab::s1"`, `"parentId":"h0123456789ab::p1"`, `"sessionId":"h0123456789ab::s2"`, `"resumeSessionId":"h0123456789ab::s4"`, `"parentSessionId":"h0123456789ab::s5"`, `"fromSession":"h0123456789ab::s6"`, `"toSession":"h0123456789ab::s7"`, `"id":"a1"`, `"turnId":"t1"`, `"jobId":"j1"`, `"agentId":"agent-1"`, `"toolUseId":"tool-1"`, `"queueId":4`, `12345678901234567890`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestParsePairingLink(t *testing.T) {
	link, err := ParsePairingLink("agenthail://pair?endpoint=https%3A%2F%2Fforge.example.ts.net%3A7412%2F&secret=abc&scope=read")
	if err != nil || link.Endpoint != "https://forge.example.ts.net:7412" || link.Secret != "abc" {
		t.Fatalf("link=%+v err=%v", link, err)
	}
	for _, bad := range []string{"https://forge/pair?secret=x", "agenthail://pair?endpoint=https://x", "agenthail://other?endpoint=https://x&secret=y"} {
		if _, err := ParsePairingLink(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func allowLoopback(t *testing.T) {
	t.Helper()
	original := TailnetPrefixes
	TailnetPrefixes = append(append([]netip.Prefix(nil), original...), netip.MustParsePrefix("127.0.0.0/8"))
	t.Cleanup(func() { TailnetPrefixes = original })
}

func TestCheckTailnetAddressAcceptsOnlyTailscaleRanges(t *testing.T) {
	for _, address := range []string{"100.64.0.1", "100.85.38.9", "100.127.255.254", "fd7a:115c:a1e0::1", "fd7a:115c:a1e0:ab12::42"} {
		if err := CheckTailnetAddress(address); err != nil {
			t.Errorf("%s rejected: %v", address, err)
		}
	}
	for _, address := range []string{"127.0.0.1", "::1", "169.254.169.254", "fe80::1", "10.0.0.5", "192.168.1.10", "172.16.0.1", "100.63.255.255", "100.128.0.1", "8.8.8.8", "224.0.0.1", "ff02::1", "fd7a:115c:a1e1::1", "fe80::1%en0", "forge.example.ts.net", ""} {
		if err := CheckTailnetAddress(address); err == nil {
			t.Errorf("%s accepted", address)
		}
	}
	if _, err := NewClient("https://forge.example.ts.net:7412", "192.168.1.10", "", ClientOptions{}); err == nil {
		t.Fatal("client accepted a LAN address")
	}
}

func TestClientClassifiesUnreachableAsNotSentAndHidesToken(t *testing.T) {
	allowLoopback(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	client, err := NewClient("https://forge.example.ts.net:"+strconv.Itoa(port), "127.0.0.1", "ahd_secret-token", ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), http.MethodPost, "/api/v1/actions", nil, []byte(`{}`), nil)
	var notSent NotSentError
	if !errors.As(err, &notSent) {
		t.Fatalf("err=%v, want NotSentError", err)
	}
	if strings.Contains(err.Error(), "ahd_secret-token") {
		t.Fatalf("error leaked token: %v", err)
	}
	if _, err := NewClient("http://forge:7412", "127.0.0.1", "", ClientOptions{}); err == nil {
		t.Fatal("plain http endpoint accepted")
	}
	if _, err := NewClient("https://forge:7412", "forge.example.ts.net", "", ClientOptions{}); err == nil {
		t.Fatal("DNS name accepted as address")
	}
}

func TestClientTreatsIntermediaryAnswerAsUnanswered(t *testing.T) {
	allowLoopback(t)
	daemonUp := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if daemonUp {
			w.Header().Set(ResponseHeader, "h0123456789ab")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	port := server.Listener.Addr().(*net.TCPAddr).Port
	client, err := NewClient("https://example.com:"+strconv.Itoa(port), "127.0.0.1", "ahd_secret-token", ClientOptions{RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(context.Background(), http.MethodPost, "/api/v1/actions", nil, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("daemon answer rejected: %v", err)
	}
	response.Body.Close()
	daemonUp = false
	_, err = client.Do(context.Background(), http.MethodPost, "/api/v1/actions", nil, []byte(`{}`), nil)
	var unanswered UnansweredError
	var notSent NotSentError
	if !errors.As(err, &unanswered) || unanswered.Status != http.StatusBadGateway || errors.As(err, &notSent) {
		t.Fatalf("err=%v, want UnansweredError and not NotSentError", err)
	}
}

func TestPairRevokesTheDeviceWhenProtocolsDoNotOverlap(t *testing.T) {
	allowLoopback(t)
	var revokedWith atomic.Value
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ResponseHeader, "h0123456789ab")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/pair":
			_, _ = w.Write([]byte(`{"token":"ahd_minted"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/version":
			_, _ = w.Write([]byte(`{"hostId":"h0123456789ab","hostName":"forge","minimumProtocol":2,"maximumProtocol":3}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/device":
			revokedWith.Store(r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	port := server.Listener.Addr().(*net.TCPAddr).Port
	link := PairingLink{Endpoint: "https://example.com:" + strconv.Itoa(port), Secret: "pair-secret"}
	token, _, err := Pair(context.Background(), link, "127.0.0.1", "mac", ClientOptions{RootCAs: roots})
	var mismatch ErrProtocolMismatch
	if !errors.As(err, &mismatch) || token != "" {
		t.Fatalf("token=%q err=%v, want protocol mismatch and no token", token, err)
	}
	if got, _ := revokedWith.Load().(string); got != "Bearer ahd_minted" {
		t.Fatalf("device revoked with %q", got)
	}
}
