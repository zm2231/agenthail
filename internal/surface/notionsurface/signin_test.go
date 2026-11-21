package notionsurface

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func TestNotionOnAComputerWithNoBrowserStatesTheLimitAndEndsSendsWithIt(t *testing.T) {
	fixture := surfacetest.NewCookieFixture(t)
	notion := NewNotion(noBrowserSignin{}, "", "")
	notion.cookieBridge = fixture.Bridge

	_, err := notion.List(context.Background())
	if !signin.IsUnavailable(err) || !strings.Contains(err.Error(), "No browser on this computer") || !strings.Contains(err.Error(), "agenthail signin use notion <profile>") {
		t.Fatalf("list err=%v", err)
	}
	_, err = notion.Send(context.Background(), &surface.Session{ID: "thread-1"}, "hello")
	if surface.DeliveryTerminalReason(err) != surface.DeliverySigninUnavailable || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("send err=%v", err)
	}
	if lines := surfacetest.ReadLines(t, fixture.BridgeLog); lines != nil {
		t.Fatalf("cookie bridge ran without a browser: %v", lines)
	}
}

func TestNotionWithSigninOffReadsNoCookiesAndSaysHowToTurnItOn(t *testing.T) {
	fixture := surfacetest.NewCookieFixture(t)
	notion := NewNotion(nil, "", "")
	notion.cookieBridge = fixture.Bridge

	_, err := notion.List(context.Background())
	if !signin.IsOff(err) || !strings.Contains(err.Error(), "Notion: browser sign-in is turned off") || !strings.Contains(err.Error(), "agenthail signin use notion auto") || !strings.Contains(err.Error(), "Settings > Browser Sign-in") {
		t.Fatalf("list err=%v", err)
	}
	_, err = notion.Send(context.Background(), &surface.Session{ID: "thread-1"}, "hello")
	if surface.DeliveryTerminalReason(err) != surface.DeliverySigninOff || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("send err=%v", err)
	}
	if lines := surfacetest.ReadLines(t, fixture.BridgeLog); lines != nil {
		t.Fatalf("cookie bridge ran without a profile: %v", lines)
	}
	if lines := surfacetest.ReadLines(t, fixture.SidecarLog); lines != nil {
		t.Fatalf("request sent without a profile: %v", lines)
	}
}

func TestNotionDetectsContextThroughTheBoundProfile(t *testing.T) {
	fixture := surfacetest.NewCookieFixture(t)
	profileDir := filepath.Join(t.TempDir(), "Profile 2")
	resolver := &switchableSignin{source: signin.Source{ProfileID: "sp_aaaaaaaaaaaa", Browser: signin.Chrome, Dir: profileDir}}
	notion := NewNotion(resolver, "", "")
	notion.cookieBridge = fixture.Bridge

	sessions, err := notion.List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "thread-1" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if account := notion.account; account == nil || account.spaceID != surfacetest.FixtureNotionSpace || account.userID != "user-1" || account.userName != "Fixture User" {
		t.Fatalf("account=%+v", account)
	}
	bridgeCalls := surfacetest.ReadLines(t, fixture.BridgeLog)
	want := fixture.Bridge + " --browser chrome --profile-dir " + profileDir + " https://app.notion.com/"
	if len(bridgeCalls) != 1 || bridgeCalls[0] != want {
		t.Fatalf("bridge calls=%q want one %q", bridgeCalls, want)
	}
	requests := surfacetest.ReadLines(t, fixture.SidecarLog)
	if len(requests) != 2 || !strings.Contains(requests[0], "/getSpaces") || !strings.Contains(requests[0], `"cookie": "sid=fixture"`) {
		t.Fatalf("requests=%q", requests)
	}

	otherDir := filepath.Join(t.TempDir(), "Default")
	resolver.source = signin.Source{ProfileID: "sp_bbbbbbbbbbbb", Browser: signin.Edge, Dir: otherDir}
	if _, err := notion.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	bridgeCalls = surfacetest.ReadLines(t, fixture.BridgeLog)
	if len(bridgeCalls) != 2 || bridgeCalls[1] != fixture.Bridge+" --browser edge --profile-dir "+otherDir+" https://app.notion.com/" {
		t.Fatalf("a new profile must read its own cookies: %q", bridgeCalls)
	}
	requests = surfacetest.ReadLines(t, fixture.SidecarLog)
	if len(requests) != 4 || !strings.Contains(requests[2], "/getSpaces") {
		t.Fatalf("a new profile must detect its own Notion account: %q", requests)
	}
}

// Two Notion accounts behind two profiles. Account A's detection blocks until
// released, so a binding switch to B happens while A's operation is in flight.
func TestNotionBindingSwitchKeepsEachOperationOnOneAccount(t *testing.T) {
	dir := t.TempDir()
	log, started, release := filepath.Join(dir, "requests.log"), filepath.Join(dir, "a-started"), filepath.Join(dir, "a-release")
	node := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in --profile-dir) shift; printf 'sid=%s' \"$(basename \"$1\")\";; esac; shift; done\n"
	surfacetest.InstallNode(t, node)
	sidecar := `import json, os, sys, time
req = json.loads(sys.stdin.read())
headers, url = req["headers"], req["url"]
sid = headers.get("cookie", "").removeprefix("sid=")
entry = {"url": url, "cookie": sid, "user": headers.get("x-notion-active-user-header", ""), "space": headers.get("x-notion-space-id", "")}
status, body = 200, {}
if url.endswith("/getSpaces"):
    if sid == "A":
        open(` + surfacetest.PyQuote(started) + `, "w").close()
        for _ in range(200):
            if os.path.exists(` + surfacetest.PyQuote(release) + `):
                break
            time.sleep(0.05)
    space = {"A": "aaaaaaaa-0000-0000-0000-000000000000", "B": "bbbbbbbb-0000-0000-0000-000000000000"}[sid]
    user = "user-" + sid
    body = {user: {"notion_user": {user: {"value": {"value": {"name": "Name " + sid}}}}, "user_settings": {user: {"value": {"value": {"settings": {"time_zone": "Zone/" + sid}}}}}, "space": {space: {}}}}
elif url.endswith("/getInferenceTranscriptsForUser"):
    body = {"transcripts": []}
elif url.endswith("/getAvailableModels"):
    body = {"models": [{"model": "almond-croissant-low", "modelMessage": "Default"}]}
elif url.endswith("/runInferenceTranscript"):
    payload = json.loads(req["body"])
    entry["context"] = next(item["value"] for item in payload["transcript"] if item["type"] == "context")
    status = 500
with open(` + surfacetest.PyQuote(log) + `, "a") as out:
    out.write(json.dumps(entry) + "\n")
sys.stdout.write(json.dumps({"status": status, "body": json.dumps(body), "error": ""}))
`
	sidecarPath := filepath.Join(dir, "sidecar.py")
	if err := os.WriteFile(sidecarPath, []byte(sidecar), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SIDECAR", sidecarPath)
	t.Setenv("AGENTHAIL_NOTION_TZ", "")

	a := signin.Source{ProfileID: "sp_aaaaaaaaaaaa", Browser: signin.Chrome, Dir: filepath.Join(dir, "A")}
	b := signin.Source{ProfileID: "sp_bbbbbbbbbbbb", Browser: signin.Chrome, Dir: filepath.Join(dir, "B")}
	resolver := &lockedSignin{source: a}
	notion := NewNotion(resolver, "", "")
	notion.cookieBridge = filepath.Join(dir, "cookie.mjs")

	listed := make(chan error, 1)
	go func() {
		_, err := notion.List(context.Background())
		listed <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("account A detection never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	resolver.use(b)
	if _, err := notion.Send(context.Background(), &surface.Session{ID: "new"}, "for B"); !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("send as B: %v", err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-listed; err != nil {
		t.Fatalf("list as A: %v", err)
	}
	resolver.use(a)
	if _, err := notion.Send(context.Background(), &surface.Session{ID: "new"}, "for A"); !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("send as A: %v", err)
	}

	sends := map[string]int{}
	for _, line := range surfacetest.ReadLines(t, log) {
		var request notionAccountRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		space := map[string]string{"A": "aaaaaaaa-0000-0000-0000-000000000000", "B": "bbbbbbbb-0000-0000-0000-000000000000"}[request.Cookie]
		if strings.HasSuffix(request.URL, "/getSpaces") {
			continue
		}
		if request.User != "user-"+request.Cookie || request.Space != space {
			t.Fatalf("request mixed accounts: %s", line)
		}
		if strings.HasSuffix(request.URL, "/runInferenceTranscript") {
			sends[request.Cookie]++
			if request.Context.UserID != request.User || request.Context.SpaceID != space || request.Context.Timezone != "Zone/"+request.Cookie {
				t.Fatalf("send context mixed accounts: %s", line)
			}
		}
	}
	if sends["A"] != 1 || sends["B"] != 1 {
		t.Fatalf("sends=%v", sends)
	}
}

type switchableSignin struct{ source signin.Source }

func (s *switchableSignin) ResolveSignin(signin.Target) (signin.Source, error) { return s.source, nil }

func (s *switchableSignin) SigninCandidates(signin.Consumer) ([]signin.Source, error) {
	return []signin.Source{s.source}, nil
}

type noBrowserSignin struct{}

func (noBrowserSignin) ResolveSignin(target signin.Target) (signin.Source, error) {
	return signin.Source{}, signin.UnavailableError{Consumer: target.Consumer}
}

func (noBrowserSignin) SigninCandidates(signin.Consumer) ([]signin.Source, error) { return nil, nil }

type lockedSignin struct {
	mu     sync.Mutex
	source signin.Source
}

func (s *lockedSignin) ResolveSignin(signin.Target) (signin.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.source, nil
}

func (s *lockedSignin) SigninCandidates(signin.Consumer) ([]signin.Source, error) {
	source, err := s.ResolveSignin(signin.Target{})
	return []signin.Source{source}, err
}

func (s *lockedSignin) use(source signin.Source) {
	s.mu.Lock()
	s.source = source
	s.mu.Unlock()
}

type notionAccountRequest struct {
	URL      string `json:"url"`
	Cookie   string `json:"cookie"`
	User     string `json:"user"`
	Space    string `json:"space"`
	Timezone string `json:"timezone"`
	Context  struct {
		UserID   string `json:"userId"`
		SpaceID  string `json:"spaceId"`
		Timezone string `json:"timezone"`
	} `json:"context"`
}
