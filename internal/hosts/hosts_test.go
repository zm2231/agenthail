package hosts

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
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
	raw := []byte(`{"ok":true,"result":{"sessionId":"s1","turnId":"t1","queueId":4},"session":{"id":"s1","surface":"codex","subagent":{"parentId":"p1","rootId":"p1"}},"attachment":{"id":"a1"},"items":[{"sessionId":"s2"}],"count":12345678901234567890}`)
	got, err := QualifyJSON("h0123456789ab", raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{`"sessionId":"h0123456789ab::s1"`, `"id":"h0123456789ab::s1"`, `"parentId":"h0123456789ab::p1"`, `"sessionId":"h0123456789ab::s2"`, `"id":"a1"`, `"turnId":"t1"`, `12345678901234567890`} {
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

func TestClientClassifiesUnreachableAsNotSentAndHidesToken(t *testing.T) {
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
