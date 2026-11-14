package surfaces

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexProbesTheDefaultDesktopPortOnlyWhereDesktopRuns(t *testing.T) {
	restore, restoreAccount := codexDesktopRunsHere, codexAccountHome
	t.Cleanup(func() { codexDesktopRunsHere, codexAccountHome = restore, restoreAccount })
	account := t.TempDir()
	codexAccountHome = func() string { return account }
	t.Setenv("HOME", account)
	t.Setenv("CODEX_HOME", "")

	codexDesktopRunsHere = true
	if got := inDaemon(NewCodex("")).desktopURL; got != "ws://127.0.0.1:9231" {
		t.Fatalf("where Desktop runs the default endpoint is %q", got)
	}

	codexDesktopRunsHere = false
	if err := inDaemon(NewCodex("")).DesktopReady(context.Background()); err == nil || !strings.Contains(err.Error(), "does not run on") {
		t.Fatalf("without a configured port: %v", err)
	}
	desktop := codextest.StartDesktop(t, func(method string) string {
		if method == "thread/loaded/list" {
			return `{"result":{"data":[]}}`
		}
		return ""
	})
	endpoint, err := url.Parse(desktop.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := inDaemon(NewCodex(endpoint.Port())).DesktopReady(context.Background()); err != nil || desktop.Lookups() != 1 {
		t.Fatalf("a configured port: err=%v lookups=%d", err, desktop.Lookups())
	}
}

func TestOnlyALocalDesktopOnAMacIsRepairedByLaunchingCodex(t *testing.T) {
	restore, restoreAccount := codexDesktopRunsHere, codexAccountHome
	t.Cleanup(func() { codexDesktopRunsHere, codexAccountHome = restore, restoreAccount })
	account := t.TempDir()
	codexAccountHome = func() string { return account }
	t.Setenv("CODEX_HOME", "")
	for _, test := range []struct {
		runsHere bool
		endpoint string
		home     string
		want     surface.RuntimeProblem
	}{
		{runsHere: true, endpoint: "", home: account, want: surface.RuntimeBridgeUnavailable},
		{runsHere: true, endpoint: "9300", home: account, want: surface.RuntimeBridgeUnavailable},
		{runsHere: true, endpoint: "ws://laptop.example:9231", home: account, want: surface.RuntimeDesktopUnreachable},
		{runsHere: false, endpoint: "9300", home: account, want: surface.RuntimeDesktopUnreachable},
		{runsHere: false, endpoint: "ws://laptop.example:9231", home: account, want: surface.RuntimeDesktopUnreachable},
	} {
		t.Setenv("HOME", test.home)
		codexDesktopRunsHere = test.runsHere
		codex := inDaemon(NewCodex(test.endpoint))
		problem, remediation := codex.desktopProblem()
		if problem != test.want || (problem == surface.RuntimeDesktopUnreachable) != strings.Contains(remediation, codex.desktopURL+" reachable") {
			t.Errorf("runsHere=%v endpoint=%q: problem=%s remediation=%q", test.runsHere, test.endpoint, problem, remediation)
		}
	}
	t.Setenv("HOME", t.TempDir())
	codexDesktopRunsHere = true
	if problem, remediation := inDaemon(NewCodex("")).desktopProblem(); problem != surface.RuntimeDesktopUnreachable || remediation != codexDesktopOptIn {
		t.Errorf("a Codex home of its own on a Mac: problem=%s remediation=%q", problem, remediation)
	}
}
