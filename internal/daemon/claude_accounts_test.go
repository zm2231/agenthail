package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestClaudeAccountsReachSessionOptionsCreationAndTheSessionRow(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	host := claudehosttest.New(t, "")
	host.Start(t)
	t.Setenv("HOME", host.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	second := filepath.Join(host.Home, ".work", ".claude")
	for path, content := range map[string]string{
		filepath.Join(host.Home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + second + `"]}`,
		filepath.Join(host.Home, ".claude.json"):              `{"oauthAccount":{"accountUuid":"a","emailAddress":"me@example.com"}}`,
		filepath.Join(second, ".claude.json"):                 `{"oauthAccount":{"accountUuid":"b","emailAddress":"work@example.com"}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := New(registry, []surface.Surface{surfaces.NewClaude(nil, host.Home)})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	call := func(method, path string, body []byte) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	var options struct {
		ClaudeAccounts []surface.ClaudeAccount `json:"claudeAccounts"`
	}
	response := call(http.MethodGet, "/api/v1/session-options", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &options); err != nil || len(options.ClaudeAccounts) != 2 {
		t.Fatalf("options=%s err=%v", response.Body.String(), err)
	}
	if work := options.ClaudeAccounts[1]; work.Name != "work" || work.Label != "work@example.com" || work.Primary {
		t.Fatalf("work=%+v", work)
	}

	body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "claude", "message": "hello", "cwd": host.Cwd, "account": "work@example.com"})
	response = call(http.MethodPost, "/api/v1/actions", body)
	var receipt struct {
		Session surface.Session `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || response.Code != http.StatusCreated {
		t.Fatalf("create=%s err=%v", response.Body.String(), err)
	}
	spawns := host.Spawns(t, receipt.Session.ID)
	if len(spawns) != 1 || spawns[0].ConfigDir == nil || *spawns[0].ConfigDir != second {
		t.Fatalf("spawns=%+v, want one start in %s", spawns, second)
	}

	transcript := filepath.Join(second, "projects", "-work", receipt.Session.ID+".jsonl")
	row := d.catalogSessionProjection(d.Surfaces[0], surface.Session{ID: receipt.Session.ID, Surface: surface.KindClaude, Transcript: transcript}, catalogIdentity{}, receipt.Session.LastActive, "", 0, false, DashboardConfig{})
	if row.ClaudeAccount == nil || row.ClaudeAccount.Name != "work" || row.ClaudeAccount.Label != "work@example.com" || row.ClaudeAccount.Root != second {
		t.Fatalf("row account=%+v", row.ClaudeAccount)
	}
	codex := d.catalogSessionProjection(d.Surfaces[0], surface.Session{ID: "c", Surface: surface.KindCodex, Transcript: transcript}, catalogIdentity{}, receipt.Session.LastActive, "", 0, false, DashboardConfig{})
	if codex.ClaudeAccount != nil {
		t.Fatalf("a Codex row carries a Claude account: %+v", codex.ClaudeAccount)
	}
}

func TestSessionMoveActionMovesAClaudeSessionAndReRegistersIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	_, registry, _, _, _ := daemonFixture(t)
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	t.Setenv("HOME", fixture.Home)
	primary := fixture.Root
	spare := fixture.AddRoot("spare")
	fixture.Root = primary
	for path, account := range map[string]string{filepath.Join(fixture.Home, ".claude.json"): "one", filepath.Join(spare, ".claude.json"): "two"} {
		data, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": account, "emailAddress": account + "@example.com"}})
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	const conversation = "3f1d2c4b-5a6e-4f70-8a91-b2c3d4e5f607"
	transcript := fixture.Transcript(conversation, fixture.Home)
	if err := registry.RegisterSession(surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: fixture.Home, Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	d := New(registry, []surface.Surface{surfaces.NewClaude(nil, fixture.Home)})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	move := func(account string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"action": "session-move", "sessionId": conversation, "account": account})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	fixture.RecordStatus(conversation, fixture.Home, "", "busy", "", time.Time{})
	if response := move("spare"); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"busy":"a turn is running"`) {
		t.Fatalf("busy move: %d %s", response.Code, response.Body.String())
	}
	fixture.RemoveRecord()

	response := move("spare")
	var reply struct {
		OK     bool               `json:"ok"`
		Result surface.MoveResult `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || response.Code != http.StatusOK || !reply.OK || reply.Result.ToRoot != spare {
		t.Fatalf("move: %d %s", response.Code, response.Body.String())
	}
	stored, err := registry.Session(conversation)
	want := filepath.Join(spare, "projects", "fixture-project", conversation+".jsonl")
	if err != nil || stored.Transcript != want {
		t.Fatalf("stored=%+v err=%v, want transcript %s", stored, err, want)
	}
}

func TestUsageLimitOfferListsOnlyAccountsThatCanTakeTheSession(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	now := time.Now()
	future := now.Add(time.Hour).UTC().Format(time.RFC3339)
	usage := func(account string, percent int) string {
		return `,"cachedUsageUtilization":{"fetchedAtMs":1,"accountUuid":"` + account + `","utilization":{"limits":[{"kind":"session","percent":` + strconv.Itoa(percent) + `,"resets_at":"` + future + `"}]}}`
	}
	shared, spare, spent, out := filepath.Join(home, ".shared"), filepath.Join(home, ".spare"), filepath.Join(home, ".spent"), filepath.Join(home, ".out")
	for path, content := range map[string]string{
		filepath.Join(home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + shared + `","` + spare + `","` + spent + `","` + out + `"]}`,
		filepath.Join(home, ".claude.json"):              `{"oauthAccount":{"accountUuid":"a","emailAddress":"me@example.com"}` + usage("a", 100) + `}`,
		filepath.Join(shared, ".claude.json"):            `{"oauthAccount":{"accountUuid":"a","emailAddress":"me@example.com"}}`,
		filepath.Join(spare, ".claude.json"):             `{"oauthAccount":{"accountUuid":"b","emailAddress":"spare@example.com"}` + usage("b", 20) + `}`,
		filepath.Join(spent, ".claude.json"):             `{"oauthAccount":{"accountUuid":"c","emailAddress":"spent@example.com"}` + usage("c", 100) + `}`,
		filepath.Join(out, ".claude.json"):               `{}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := New(registry, []surface.Surface{surfaces.NewClaude(nil, home)})
	session := &surface.Session{ID: "s", Surface: surface.KindClaude, Transcript: filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")}
	limit := surface.TimelineItem{Kind: "notice", Status: surface.TimelineUsageLimit, Text: "You've hit your session limit", ResetsAt: future}
	offer := d.usageLimitOffer(session, []surface.TimelineItem{limit}, now)
	if offer == nil || offer.Account == nil || offer.Account.Root != filepath.Join(home, ".claude") || offer.Message != limit.Text {
		t.Fatalf("offer=%+v", offer)
	}
	if len(offer.Alternatives) != 1 || offer.Alternatives[0].Root != spare {
		t.Fatalf("alternatives=%+v, want only %s", offer.Alternatives, spare)
	}
	if d.usageLimitOffer(session, []surface.TimelineItem{limit, {Kind: "message", Role: "assistant", Text: "back"}}, now) != nil {
		t.Fatal("an answered limit still offers a move")
	}
	if d.usageLimitOffer(&surface.Session{ID: "c", Surface: surface.KindCodex}, []surface.TimelineItem{limit}, now) != nil {
		t.Fatal("a Codex session was offered a Claude account")
	}
}
