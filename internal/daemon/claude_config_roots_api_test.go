package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestClaudeRootsAPIReportsEachRootsSigninAndLiveUsage(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": "acct-work", "emailAddress": "work@example.com", "organizationUuid": "org-work"}})
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	profiles := []signin.Source{{ProfileID: "sp_personal", Label: "personal", Browser: signin.Chrome, Dir: "/personal"}, {ProfileID: "sp_work", Label: "work", Browser: signin.Chrome, Dir: "/work"}}
	var claudeCalls atomic.Int32
	claude := surfaces.NewClaudeWithRequest(candidates(profiles), home, func(_ context.Context, _ string, url string, _ map[string]string, _ string, cookies surfaces.CookieSource, _ time.Duration) (int, string, error) {
		claudeCalls.Add(1)
		switch {
		case strings.HasSuffix(url, "/api/account"):
			return 200, `{"uuid":"acct` + strings.ReplaceAll(cookies.Signin.Dir, "/", "-") + `"}`, nil
		case strings.HasSuffix(url, "/api/organizations/org-work/usage") && cookies.Signin.Dir == "/work":
			return 200, `{"limits":[{"kind":"weekly_all","percent":98,"severity":"critical","resets_at":"2099-01-01T00:00:00Z"}]}`, nil
		}
		return 404, "{}", nil
	})
	d.Surfaces = []surface.Surface{claude}
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}

	var listing struct {
		Claude []surface.ClaudeConfigRoot `json:"claude"`
	}
	if code := api.do(http.MethodGet, "/api/v1/claude/roots", "", &listing); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if len(listing.Claude) != 1 {
		t.Fatalf("listing=%+v", listing)
	}
	root := listing.Claude[0]
	if root.SignedInAs == nil || root.SignedInAs.Email != "work@example.com" || root.Signin != "work" || root.Usage == nil || len(root.Usage.Limits) != 1 || root.Usage.Limits[0].Percent != 98 {
		t.Fatalf("root=%+v", root)
	}

	var options struct {
		ClaudeConfigRoots []surface.ClaudeConfigRoot `json:"claudeConfigRoots"`
	}
	before := claudeCalls.Load()
	if code := api.do(http.MethodGet, "/api/v1/session-options", "", &options); code != http.StatusOK {
		t.Fatalf("session-options status=%d", code)
	}
	if claudeCalls.Load() != before || len(options.ClaudeConfigRoots) != 1 || options.ClaudeConfigRoots[0].Usage != nil {
		t.Fatalf("the new-session form must get the roots without waiting on claude.ai: calls=%d roots=%+v", claudeCalls.Load()-before, options.ClaudeConfigRoots)
	}
	var usage struct {
		Root         string               `json:"root"`
		Usage        *surface.ClaudeUsage `json:"usage"`
		UsageProblem string               `json:"usageProblem"`
	}
	if code := api.do(http.MethodGet, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(options.ClaudeConfigRoots[0].Root), "", &usage); code != http.StatusOK {
		t.Fatalf("claude-usage status=%d", code)
	}
	if usage.Root != options.ClaudeConfigRoots[0].Root || usage.Usage == nil || usage.Usage.Limits[0].Percent != 98 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeRootUsageSaysALoginHasNoProfileWithoutNamingTheProfiles(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": "acct-work", "emailAddress": "work@example.com", "organizationUuid": "org-work"}})
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	profiles := []signin.Source{{ProfileID: "sp_personal", Label: "personal", Browser: signin.Chrome, Dir: "/personal"}}
	d.Surfaces = []surface.Surface{surfaces.NewClaudeWithRequest(candidates(profiles), home, func(context.Context, string, string, map[string]string, string, surfaces.CookieSource, time.Duration) (int, string, error) {
		return 200, `{"uuid":"acct-personal","email_address":"personal@example.com"}`, nil
	})}
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	var usage struct {
		Usage        *surface.ClaudeUsage `json:"usage"`
		UsageProblem string               `json:"usageProblem"`
	}
	if code := api.do(http.MethodGet, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(filepath.Join(home, ".claude")), "", &usage); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if usage.Usage != nil || usage.UsageProblem == "" || strings.Contains(usage.UsageProblem, "personal") {
		t.Fatalf("usage=%+v", usage)
	}
	if code := api.do(http.MethodGet, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(filepath.Join(home, "elsewhere")), "", &usage); code != http.StatusNotFound {
		t.Fatalf("an unconfigured root answered %d", code)
	}
}

type candidates []signin.Source

func (c candidates) ResolveSignin(signin.Target) (signin.Source, error)        { return c[0], nil }
func (c candidates) SigninCandidates(signin.Consumer) ([]signin.Source, error) { return c, nil }

func TestClaudeRootUsageSaysClaudeAICouldNotBeAskedWhenTheAccountCheckFails(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": "acct-work", "emailAddress": "work@example.com", "organizationUuid": "org-work"}})
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	d.Surfaces = []surface.Surface{surfaces.NewClaudeWithRequest(candidates([]signin.Source{{ProfileID: "sp_work", Label: "work", Browser: signin.Chrome, Dir: "/work"}}), home, func(context.Context, string, string, map[string]string, string, surfaces.CookieSource, time.Duration) (int, string, error) {
		return 503, `{"type":"error"}`, nil
	})}
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	var usage struct {
		UsageProblem string `json:"usageProblem"`
	}
	if code := api.do(http.MethodGet, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(filepath.Join(home, ".claude")), "", &usage); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if !strings.Contains(usage.UsageProblem, "could not be asked") || strings.Contains(usage.UsageProblem, "no browser sign-in profile") {
		t.Fatalf("an outage is not a missing profile: %q", usage.UsageProblem)
	}
}
