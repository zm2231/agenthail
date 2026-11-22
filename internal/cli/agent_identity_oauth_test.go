package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestThePersonTurnsOAuthOnListsItsGrantsAndRevokesOne(t *testing.T) {
	app, r := cliFixture(t, &cliSurface{})
	run := func(args ...string) (string, error) {
		return captureStdout(t, func() error { return app.Run(append([]string{"identity", "oauth"}, args...)) })
	}
	if _, err := run("set", "--url", "http://agent.example.com"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("a plain-http public URL was accepted: %v", err)
	}
	if out, err := run("set", "--url", "https://agent.example.com"); err != nil || !strings.Contains(out, "Give a client https://agent.example.com/mcp") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	identity, err := r.AddAgentIdentity("scout")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.BindRemoteIdentity("scout", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetIdentityOAuth("scout", true); err != nil {
		t.Fatal(err)
	}
	client, err := r.RegisterOAuthClient("Kat", []string{"https://kat.example/cb"}, 64, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := r.IssueOAuthGrant(identity.SessionID, client.ID, registry.OAuthLifetimes{Access: time.Hour, Refresh: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := captureStdout(t, func() error { return app.Run([]string{"identity", "list"}) }); err != nil || !strings.Contains(out, "oauth: Kat") {
		t.Fatalf("identity list: out=%q err=%v", out, err)
	}
	if out, err := run("revoke", tokens.Grant.ID); err != nil || !strings.Contains(out, "revoked @scout's grant to Kat") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, _, err := r.OAuthGrantByAccess(tokens.Access); err == nil {
		t.Fatal("the revoked grant's token still works")
	}
	if err := r.SetAccessSettings("example-team", "aud"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(); err != nil || !strings.Contains(out, "OAuth: on at") {
		t.Fatalf("with Access settings set: out=%q err=%v", out, err)
	}
}
