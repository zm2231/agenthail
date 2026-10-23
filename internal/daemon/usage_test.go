package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type usageBody struct {
	Sets []usageSet `json:"sets"`
}

func TestUsageGivesEachClaudeAccountItsOwnSetPrimaryFirst(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	team := filepath.Join(home, ".team", ".claude")
	for path, content := range map[string]string{
		filepath.Join(home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + team + `"]}`,
		filepath.Join(home, ".claude.json"):              `{"oauthAccount":{"accountUuid":"acct-work","emailAddress":"work@example.com","organizationUuid":"org-work"}}`,
		filepath.Join(team, ".claude.json"):              `{"oauthAccount":{"accountUuid":"acct-team","emailAddress":"team@example.com","organizationUuid":"org-team"}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	resets := time.Now().Add(50 * time.Hour).UTC().Format(time.RFC3339)
	profiles := []signin.Source{{ProfileID: "sp_work", Label: "work", Browser: signin.Chrome, Dir: "/work"}}
	d.Surfaces = []surface.Surface{surfaces.NewClaudeWithRequest(candidates(profiles), home, func(_ context.Context, _ string, url string, _ map[string]string, _ string, _ surfaces.CookieSource, _ time.Duration) (int, string, error) {
		switch {
		case strings.HasSuffix(url, "/api/account"):
			return 200, `{"uuid":"acct-work"}`, nil
		case strings.HasSuffix(url, "/api/organizations/org-work/usage"):
			return 200, `{"limits":[{"kind":"weekly_all","percent":62,"resets_at":"` + resets + `"}]}`, nil
		}
		return 404, "{}", nil
	})}
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	var body usageBody
	if code := api.do(http.MethodGet, "/api/v1/usage", "", &body); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if len(body.Sets) != 2 {
		t.Fatalf("one set per Claude account: %+v", body.Sets)
	}
	work, other := body.Sets[0], body.Sets[1]
	if !work.Primary || work.Account != "work@example.com" || work.Provider != "claude" || work.Problem != "" || len(work.Meters) != 1 || work.Meters[0].Label != "Claude week" || work.Meters[0].Percent != 62 {
		t.Fatalf("the primary account comes first with its meters: %+v", work)
	}
	if other.Primary || other.Account != "team@example.com" || other.Root != team || len(other.Meters) != 0 || other.Problem == "" {
		t.Fatalf("an account no browser profile holds says why it has no meters: %+v", other)
	}
	for _, set := range body.Sets {
		if set.HostID != d.localHost().ID || set.Host != d.localHost().Name || !set.Local {
			t.Fatalf("a local set names this host: %+v", set)
		}
	}
}

func TestUsageNamesTheProblemWhenClaudeAIIsUnreachable(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"acct-work","emailAddress":"work@example.com","organizationUuid":"org-work"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := []signin.Source{{ProfileID: "sp_work", Label: "work", Browser: signin.Chrome, Dir: "/work"}}
	d.Surfaces = []surface.Surface{surfaces.NewClaudeWithRequest(candidates(profiles), home, func(context.Context, string, string, map[string]string, string, surfaces.CookieSource, time.Duration) (int, string, error) {
		return 503, `{"type":"error"}`, nil
	})}
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	var body usageBody
	if code := api.do(http.MethodGet, "/api/v1/usage", "", &body); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if len(body.Sets) != 1 || body.Sets[0].Meters == nil || len(body.Sets[0].Meters) != 0 || body.Sets[0].Problem == "" {
		t.Fatalf("an unreachable claude.ai gives a problem, not a meter: %+v", body.Sets)
	}
}
