package surfaces

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/browsercookie"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func TestAProfileThatAnswers401OnceIsAskedAgainOnFreshCookiesAndTheMessageGoesOutOnce(t *testing.T) {
	root := newClaudeRootFixture(t, "c", "c@example.com")
	dir := t.TempDir()
	bridge, bridgeLog, sidecarLog, probes := filepath.Join(dir, "cookie.mjs"), filepath.Join(dir, "bridge.log"), filepath.Join(dir, "sidecar.log"), filepath.Join(dir, "probes")
	if err := os.WriteFile(bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	surfacetest.InstallNode(t, "#!/bin/sh\necho read >> "+surfacetest.ShellQuote(bridgeLog)+"\nprintf 'sessionKey=rotated'\n")
	sidecar := `import json, os, sys
req = json.loads(sys.stdin.read())
with open(` + surfacetest.PyQuote(sidecarLog) + `, "a") as log:
    log.write(req["method"] + " " + req["url"] + "\n")
status, body = 200, {}
if req["url"].endswith("/api/account"):
    first = not os.path.exists(` + surfacetest.PyQuote(probes) + `)
    open(` + surfacetest.PyQuote(probes) + `, "a").close()
    status, body = (401, {"error": {"type": "authentication_error"}}) if first else (200, {"uuid": "c", "email_address": "c@example.com"})
sys.stdout.write(json.dumps({"status": status, "body": json.dumps(body), "error": ""}))
`
	sidecarPath := filepath.Join(dir, "sidecar.py")
	if err := os.WriteFile(sidecarPath, []byte(sidecar), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SIDECAR", sidecarPath)
	t.Setenv("AGENTHAIL_COOKIE_BRIDGE", bridge)
	profile := candidateSignin{sources: []signin.Source{{ProfileID: "sp_c", Label: "choosecadence", Browser: signin.Chrome, Dir: "/c"}}}
	claude := NewClaude(profile, root.home)
	session := root.session
	session.Transport, session.PID = "uds", os.Getpid()

	if _, err := claude.postMessage(context.Background(), session, "from the phone"); err != nil {
		t.Fatalf("err=%v, want the message sent once the profile answers on fresh cookies", err)
	}
	requests := surfacetest.ReadLines(t, sidecarLog)
	want := []string{"GET https://claude.ai/api/account", "GET https://claude.ai/api/account", "POST https://claude.ai/v1/code/sessions/cse_fixture/events"}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests=%q, want the account asked twice and the message posted once", requests)
	}
	if reads := surfacetest.ReadLines(t, bridgeLog); len(reads) != 2 {
		t.Fatalf("cookie reads=%d, want a fresh read after the 401", len(reads))
	}
	if route := claude.Route(session, surface.AuthorPerson); route != surface.RouteHumanTurn {
		t.Fatalf("route=%v, want Remote Control kept: the sign-in check passed", route)
	}
}

func TestAProfileStillRefusedOnFreshCookiesAfterAnsweringForTheAccountLeavesTheAnswerOpen(t *testing.T) {
	root := newClaudeRootFixture(t, "c", "c@example.com")
	refuse := false
	request := func(ctx context.Context, method, url string, headers map[string]string, body string, cookies browsercookie.Source, timeout time.Duration) (int, string, error) {
		if refuse && strings.HasSuffix(url, "/api/account") {
			return 401, `{"error":{"type":"authentication_error"}}`, nil
		}
		if strings.HasSuffix(url, "/api/account") {
			return 200, `{"uuid":"c","email_address":"c@example.com"}`, nil
		}
		return 200, "{}", nil
	}
	profile := candidateSignin{sources: []signin.Source{{ProfileID: "sp_c", Label: "choosecadence", Browser: signin.Chrome, Dir: "/c"}}}
	claude := NewClaudeWithRequest(profile, root.home, request)
	if _, err := claude.postMessage(context.Background(), root.session, "first"); err != nil {
		t.Fatal(err)
	}
	claude.webAccounts.mu.Lock()
	for key, entry := range claude.webAccounts.byProfile {
		entry.expiresAt = time.Now().Add(-time.Second)
		claude.webAccounts.byProfile[key] = entry
	}
	claude.webAccounts.mu.Unlock()
	refuse = true

	_, err := claude.postMessage(context.Background(), root.session, "second")
	if surface.IsDeliveryTerminal(err) || !surface.IsDeliveryUnavailable(err) || !strings.Contains(err.Error(), "choosecadence answered") {
		t.Fatalf("err=%v, want the message held: the profile answered for this account a moment ago", err)
	}
}

func TestTheSigninCheckLogSaysACookieReadFailedRatherThanThatCookiesWereUsed(t *testing.T) {
	root := newClaudeRootFixture(t, "c", "c@example.com")
	dir := t.TempDir()
	bridge := filepath.Join(dir, "cookie.mjs")
	if err := os.WriteFile(bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	surfacetest.InstallNode(t, "#!/bin/sh\necho 'profile is locked' >&2\nexit 1\n")
	sidecar := filepath.Join(dir, "sidecar.py")
	if err := os.WriteFile(sidecar, []byte("import sys\nsys.exit(1)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SIDECAR", sidecar)
	t.Setenv("AGENTHAIL_COOKIE_BRIDGE", bridge)
	var logged strings.Builder
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	claude := NewClaude(candidateSignin{sources: []signin.Source{{ProfileID: "sp_c", Label: "choosecadence", Browser: signin.Chrome, Dir: "/c"}}}, root.home)

	for range 2 {
		if _, err := claude.postMessage(context.Background(), root.session, "hello"); err == nil {
			t.Fatal("a message went out with no cookies")
		}
	}
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "reading them from the browser failed") || !strings.Contains(lines[1], "the cached result is a failed read") {
		t.Fatalf("log=%q, want a failed browser read and then the cached failure", lines)
	}
	if strings.Contains(logged.String(), "cookies cached") || strings.Contains(logged.String(), "read fresh") {
		t.Fatalf("log=%q claims cookies were used", lines)
	}
}
