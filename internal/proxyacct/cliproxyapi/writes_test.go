package cliproxyapi

import (
	"context"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/proxyacct/proxyaccttest"
)

func TestSetPreferenceWritesOneAccountsField(t *testing.T) {
	proxy := proxyaccttest.New(t)
	client := New(proxy.Server.URL, keyFile(t, proxyaccttest.Key, 0o600), nil)
	if err := client.SetPreference(context.Background(), "claude-team.json", proxyacct.Preferred); err != nil {
		t.Fatal(err)
	}
	if err := client.ReleaseSessions(context.Background(), "claude-dev.json"); err != nil {
		t.Fatal(err)
	}
	writes := proxy.Writes()
	if len(writes) != 2 || writes[0] != `fields {"name":"claude-team.json","quota_preference":10}` || writes[1] != `release {"name":"claude-dev.json"}` {
		t.Fatalf("writes=%q", writes)
	}
	accounts, err := client.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.ID == "claude-team.json" && account.Preference != 10 {
			t.Fatalf("team=%+v", account)
		}
	}
}

func TestAWriteRefusalLatchesTheKeyLikeAReadOne(t *testing.T) {
	proxy := proxyaccttest.New(t)
	client := New(proxy.Server.URL, keyFile(t, "wrong-key", 0o600), nil)
	for range 3 {
		err := client.SetPreference(context.Background(), "claude-team.json", proxyacct.Preferred)
		if proxyacct.ProblemOf(err) != proxyacct.KeyRefused || strings.Contains(err.Error(), "wrong-key") {
			t.Fatalf("err=%v", err)
		}
	}
	if _, err := client.Accounts(context.Background()); proxyacct.ProblemOf(err) != proxyacct.KeyRefused {
		t.Fatalf("err=%v", err)
	}
	if got := proxy.Attempts.Load(); got != 1 {
		t.Fatalf("the refused key was offered %d times", got)
	}
}

func TestAReadOnlyKeyIsNamedAndNotLatched(t *testing.T) {
	proxy := proxyaccttest.New(t)
	client := New(proxy.Server.URL, keyFile(t, proxyaccttest.Key, 0o600), nil)
	proxy.Forced.Store(`403 {"error":"read-only management key"}`)
	err := client.SetPreference(context.Background(), "claude-team.json", proxyacct.Preferred)
	if proxyacct.ProblemOf(err) != proxyacct.ReadOnly {
		t.Fatalf("err=%v", err)
	}
	proxy.Forced.Store("")
	if _, err := client.Accounts(context.Background()); err != nil {
		t.Fatalf("a read-only refusal must not stop reads: %v", err)
	}
}

func TestAProxyErrorThatEchoesTheKeyNeverCarriesIt(t *testing.T) {
	proxy := proxyaccttest.New(t)
	client := New(proxy.Server.URL, keyFile(t, proxyaccttest.Key, 0o600), nil)
	proxy.FailWrite.Store(`claude-dev.json 500 {"error":"bad header Bearer ` + proxyaccttest.Key + `"}`)
	err := client.SetPreference(context.Background(), "claude-dev.json", proxyacct.NotPreferred)
	if err == nil || strings.Contains(err.Error(), proxyaccttest.Key) || !strings.Contains(err.Error(), "Bearer [key]") {
		t.Fatalf("err=%v", err)
	}
	proxy.Forced.Store(`500 {"error":"` + proxyaccttest.Key + `"}`)
	if _, err := client.Accounts(context.Background()); err == nil || strings.Contains(err.Error(), proxyaccttest.Key) {
		t.Fatalf("err=%v", err)
	}
}

func TestAWriteToAMissingAccountSaysSo(t *testing.T) {
	proxy := proxyaccttest.New(t)
	client := New(proxy.Server.URL, keyFile(t, proxyaccttest.Key, 0o600), nil)
	if err := client.SetPreference(context.Background(), "gone.json", proxyacct.Preferred); proxyacct.ProblemOf(err) != proxyacct.NoAccount {
		t.Fatalf("err=%v", err)
	}
	proxy.FailWrite.Store(`claude-dev.json 500 {"error":"failed to persist auth file"}`)
	err := client.SetPreference(context.Background(), "claude-dev.json", proxyacct.NotPreferred)
	if proxyacct.ProblemOf(err) != proxyacct.UnknownAnswer || !strings.Contains(err.Error(), "failed to persist auth file") {
		t.Fatalf("err=%v", err)
	}
}
