package cliproxyapi

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/proxyacct/proxyaccttest"
)

func keyFile(t *testing.T, key string, mode os.FileMode) *proxyacct.KeyRef {
	return &proxyacct.KeyRef{File: proxyaccttest.KeyFile(t, t.TempDir(), key, mode)}
}

func TestAKeyFromACommandReadsTheProxy(t *testing.T) {
	proxy := proxyaccttest.New(t)
	key := &proxyacct.KeyRef{Command: []string{"/bin/sh", "-c", `printf '%s\n' "$0"`, proxyaccttest.Key}}
	if _, err := New(proxy.Server.URL, key, nil).Accounts(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAccountsReadEveryClaudeAndCodexCredential(t *testing.T) {
	proxy := proxyaccttest.New(t)
	client := New(proxy.Server.URL, keyFile(t, proxyaccttest.Key, 0o600), nil)
	client.now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
	if err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	accounts, err := client.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 4 {
		t.Fatalf("accounts=%+v", accounts)
	}
	dev := accounts[0]
	if dev.Label != "dev@example.com" || dev.Status != proxyacct.StatusActive || dev.Preference != 10 || len(dev.Windows) != 3 {
		t.Fatalf("dev=%+v", dev)
	}
	session, week, model := dev.Windows[0], dev.Windows[1], dev.Windows[2]
	if session.Kind != proxyacct.WindowSession || session.Percent != 18 || session.Ceiling != 60 || session.ResetsAt.Unix() != 1793016000 || !session.ObservedAt.Equal(time.Date(2026, 10, 26, 11, 58, 0, 0, time.UTC)) {
		t.Fatalf("the credential's newer reading wins over an older model reading: %+v", session)
	}
	if week.Kind != proxyacct.WindowWeek || week.Percent != 41 || model.Kind != proxyacct.WindowWeekModel || model.Percent != 70 {
		t.Fatalf("week=%+v model=%+v", week, model)
	}
	if old := accounts[1]; old.Label != "old" || old.Status != proxyacct.StatusDisabled {
		t.Fatalf("old=%+v", old)
	}
	if team := accounts[2]; team.Status != proxyacct.StatusCooling || team.NextRetry.Year() != 2099 || len(team.Windows) != 0 {
		t.Fatalf("team=%+v", team)
	}
	codex := accounts[3]
	if codex.Provider != "codex" || len(codex.Windows) != 2 || codex.Windows[0].Kind != proxyacct.WindowSession || codex.Windows[0].Percent != 12 || codex.Windows[1].Kind != proxyacct.WindowWeek {
		t.Fatalf("codex=%+v", codex)
	}
}

func TestARefusedKeyIsNotOfferedAgainUntilItChanges(t *testing.T) {
	proxy := proxyaccttest.New(t)
	ref := keyFile(t, "wrong-key", 0o600)
	client := New(proxy.Server.URL, ref, nil)
	for range 6 {
		_, err := client.Accounts(context.Background())
		if proxyacct.ProblemOf(err) != proxyacct.KeyRefused || strings.Contains(err.Error(), "wrong-key") {
			t.Fatalf("err=%v", err)
		}
	}
	if n := proxy.Attempts.Load(); n != 1 {
		t.Fatalf("a refused key was offered %d times", n)
	}
	if err := os.WriteFile(ref.File, []byte(proxyaccttest.Key), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Accounts(context.Background()); err != nil || proxy.Attempts.Load() != 2 {
		t.Fatalf("a changed key is tried at once: err=%v attempts=%d", err, proxy.Attempts.Load())
	}
}

func TestManagementRefusalsNameTheirCause(t *testing.T) {
	for answer, want := range map[string]proxyacct.Problem{
		`403 {"error":"remote management disabled"}`:    proxyacct.RemoteOff,
		`403 {"error":"remote management key not set"}`: proxyacct.NoManagement,
		`404 {}`: proxyacct.NoManagement,
		`403 {"error":"IP banned due to too many failed attempts. Try again in 29m"}`: proxyacct.Banned,
	} {
		proxy := proxyaccttest.New(t)
		proxy.Forced.Store(answer)
		client := New(proxy.Server.URL, keyFile(t, proxyaccttest.Key, 0o600), nil)
		if _, err := client.Accounts(context.Background()); proxyacct.ProblemOf(err) != want {
			t.Fatalf("%s: err=%v", answer, err)
		}
		if want == proxyacct.Banned {
			client.Accounts(context.Background())
			if n := proxy.Attempts.Load(); n != 1 {
				t.Fatalf("a ban is waited out, not retried: %d attempts", n)
			}
		}
	}
}

func TestAKeyFileOthersCanReadIsNotUsed(t *testing.T) {
	proxy := proxyaccttest.New(t)
	client := New(proxy.Server.URL, keyFile(t, proxyaccttest.Key, 0o644), nil)
	_, err := client.Accounts(context.Background())
	if proxyacct.ProblemOf(err) != proxyacct.KeyUnreadable || !strings.Contains(err.Error(), "chmod 600") || proxy.Attempts.Load() != 0 {
		t.Fatalf("err=%v attempts=%d", err, proxy.Attempts.Load())
	}
}

func TestAnUnreachableProxyIsAHealthProblem(t *testing.T) {
	proxy := proxyaccttest.New(t)
	proxy.Server.Close()
	client := New(proxy.Server.URL, keyFile(t, proxyaccttest.Key, 0o600), nil)
	if err := client.Health(context.Background()); proxyacct.ProblemOf(err) != proxyacct.Unreachable {
		t.Fatalf("err=%v", err)
	}
	if _, err := client.Accounts(context.Background()); proxyacct.ProblemOf(err) != proxyacct.Unreachable || strings.Contains(err.Error(), proxyaccttest.Key) {
		t.Fatalf("err=%v", err)
	}
}
