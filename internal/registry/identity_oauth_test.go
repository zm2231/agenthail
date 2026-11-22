package registry

import (
	"errors"
	"testing"
	"time"
)

var testOAuthLife = OAuthLifetimes{Access: time.Hour, Refresh: 30 * 24 * time.Hour}

func oauthFixture(t *testing.T) (*Registry, AgentIdentity, OAuthClient) {
	t.Helper()
	r := openTestRegistry(t)
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
	if err := r.SetOAuthIssuer("https://agent.example.com"); err != nil {
		t.Fatal(err)
	}
	client, err := r.RegisterOAuthClient("Kat", []string{"https://kat.example/callback"}, 64, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return r, identity, client
}

func TestARefreshTokenPresentedTwiceRevokesItsGrant(t *testing.T) {
	r, identity, client := oauthFixture(t)
	first, err := r.IssueOAuthGrant(identity.SessionID, client.ID, testOAuthLife)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.RefreshOAuthGrant(first.Refresh, client.ID, testOAuthLife)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.OAuthGrantByAccess(first.Access); !errors.Is(err, ErrOAuthUnknown) {
		t.Fatalf("the access token a refresh replaced still works: %v", err)
	}
	if grant, _, err := r.OAuthGrantByAccess(second.Access); err != nil || grant.IdentityID != identity.SessionID {
		t.Fatalf("grant=%+v err=%v", grant, err)
	}
	if _, err := r.RefreshOAuthGrant(second.Refresh, "ahc_other", testOAuthLife); !errors.Is(err, ErrOAuthUnknown) {
		t.Fatalf("another client refreshed the grant: %v", err)
	}
	epoch, err := r.RemoteEpoch()
	if err != nil {
		t.Fatal(err)
	}
	reused, err := r.RefreshOAuthGrant(first.Refresh, client.ID, testOAuthLife)
	if !errors.Is(err, ErrOAuthRefreshReused) || reused.Grant.ID != first.Grant.ID {
		t.Fatalf("reused=%+v err=%v", reused.Grant, err)
	}
	if _, _, err := r.OAuthGrantByAccess(second.Access); !errors.Is(err, ErrOAuthUnknown) {
		t.Fatalf("the grant survived a reused refresh token: %v", err)
	}
	if _, err := r.RefreshOAuthGrant(second.Refresh, client.ID, testOAuthLife); !errors.Is(err, ErrOAuthUnknown) {
		t.Fatalf("the latest refresh token outlived its grant: %v", err)
	}
	if after, _ := r.RemoteEpoch(); after <= epoch {
		t.Fatalf("revoking a grant left the remote epoch at %d", after)
	}
}

func TestAGrantEndsWithRemoteAccessOrTheIssuerButNotWithATokenRotation(t *testing.T) {
	cases := []struct {
		name  string
		ends  bool
		apply func(*Registry) error
	}{
		{"remote token rotated", false, func(r *Registry) error { _, _, err := r.RotateRemoteIdentity("scout"); return err }},
		{"same issuer set again", false, func(r *Registry) error { return r.SetOAuthIssuer("https://agent.example.com") }},
		{"remote access turned off", true, func(r *Registry) error { return r.UnbindRemoteIdentity("scout") }},
		{"OAuth sign-in turned off", true, func(r *Registry) error { _, err := r.SetIdentityOAuth("scout", false); return err }},
		{"identity removed", true, func(r *Registry) error { return r.RemoveAgentIdentity("scout") }},
		{"issuer changed", true, func(r *Registry) error { return r.SetOAuthIssuer("https://other.example.com") }},
		{"issuer cleared", true, func(r *Registry) error { return r.ClearOAuthIssuer() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, identity, client := oauthFixture(t)
			tokens, err := r.IssueOAuthGrant(identity.SessionID, client.ID, testOAuthLife)
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.apply(r); err != nil {
				t.Fatal(err)
			}
			_, _, err = r.OAuthGrantByAccess(tokens.Access)
			if ended := errors.Is(err, ErrOAuthUnknown); ended != tc.ends {
				t.Fatalf("ended=%v want %v (err=%v)", ended, tc.ends, err)
			}
		})
	}
}

func TestAGrantsEventSubscriptionLastsAsLongAsTheGrant(t *testing.T) {
	r, identity, client := oauthFixture(t)
	tokens, err := r.IssueOAuthGrant(identity.SessionID, client.ID, testOAuthLife)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := r.RemoteEpoch()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SubscribeEvent(EventSubscribe{ID: "sub_kat", IdentityID: identity.SessionID, TokenHash: tokens.Grant.Credential(), Epoch: epoch, Name: "inbox.unread", Args: "{}", URL: "https://kat.example/hook", Secret: "whsec_x", Verified: true, Limit: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RefreshOAuthGrant(tokens.Refresh, client.ID, testOAuthLife); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RotateRemoteIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	if _, live, err := r.LiveEventSubscription("sub_kat"); err != nil || !live {
		t.Fatalf("a refresh or the remote token's rotation ended the grant's subscription: live=%v err=%v", live, err)
	}
	if _, err := r.RevokeOAuthGrant(tokens.Grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, live, err := r.LiveEventSubscription("sub_kat"); err != nil || live {
		t.Fatalf("the subscription outlived its grant: live=%v err=%v", live, err)
	}
}

func TestUnapprovedClientsAreCappedAndPruned(t *testing.T) {
	r, identity, approved := oauthFixture(t)
	if _, err := r.IssueOAuthGrant(identity.SessionID, approved.ID, testOAuthLife); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterOAuthClient("a", []string{"https://a.example/cb"}, 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterOAuthClient("b", []string{"https://b.example/cb"}, 1, time.Hour); !errors.Is(err, ErrOAuthClientLimit) {
		t.Fatalf("a second unapproved client got in past a limit of one: %v", err)
	}
	later := time.Now().Add(2 * time.Hour)
	r.now = func() time.Time { return later }
	if _, err := r.RegisterOAuthClient("c", []string{"https://c.example/cb"}, 1, time.Hour); err != nil {
		t.Fatalf("a stale unapproved client was not pruned: %v", err)
	}
	if _, err := r.OAuthClient(approved.ID); err != nil {
		t.Fatalf("pruning removed an approved client: %v", err)
	}
}
