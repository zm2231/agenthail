package claudesurface

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/browsercookie"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

type unreadableSignin struct{}

func (unreadableSignin) ResolveSignin(target signin.Target) (signin.Source, error) {
	return signin.Source{}, signin.ProblemError{Consumer: target.Consumer, Err: errors.New("permission denied reading Safari data at /fixture/Cookies.binarycookies")}
}

func (u unreadableSignin) SigninCandidates(consumer signin.Consumer) ([]signin.Source, error) {
	_, err := u.ResolveSignin(signin.Target{Consumer: consumer})
	return nil, err
}

func TestClaudeRemoteControlWithAnUnreadableProfileFailsBeforePosting(t *testing.T) {
	posted := false
	claude := NewClaudeWithRequest(unreadableSignin{}, t.TempDir(), func(context.Context, string, string, map[string]string, string, browsercookie.Source, time.Duration) (int, string, error) {
		posted = true
		return 200, "{}", nil
	})
	_, err := claude.postMessage(context.Background(), &surface.Session{ID: "conversation", Bridge: "session_fixture"}, "hello")
	if surface.DeliveryTerminalReason(err) != surface.DeliverySigninUnreadable || !strings.Contains(err.Error(), "permission denied reading Safari data") || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
	if posted {
		t.Fatal("posted to claude.ai without a readable sign-in profile")
	}
}

func TestClaudeRemoteControlWithSigninOffFailsBeforePosting(t *testing.T) {
	posted := false
	claude := NewClaudeWithRequest(nil, t.TempDir(), func(context.Context, string, string, map[string]string, string, browsercookie.Source, time.Duration) (int, string, error) {
		posted = true
		return 200, "{}", nil
	})
	_, err := claude.postMessage(context.Background(), &surface.Session{ID: "conversation", Bridge: "session_fixture"}, "hello")
	if surface.DeliveryTerminalReason(err) != surface.DeliverySigninOff || !strings.Contains(err.Error(), "agenthail signin use claude auto") {
		t.Fatalf("err=%v", err)
	}
	if posted {
		t.Fatal("posted to claude.ai without a sign-in profile")
	}
}

func TestClaudeRemoteControlPassesTheResolvedProfile(t *testing.T) {
	var got browsercookie.Source
	source := signin.Source{ProfileID: "sp_cccccccccccc", Browser: signin.Firefox, Dir: "/fixture/firefox/abc.default"}
	claude := NewClaudeWithRequest(signin.Fixed(source), t.TempDir(), func(_ context.Context, _, _ string, _ map[string]string, _ string, cookies browsercookie.Source, _ time.Duration) (int, string, error) {
		got = cookies
		return 200, "{}", nil
	})
	if _, err := claude.postMessage(context.Background(), &surface.Session{ID: "conversation", Bridge: "session_fixture"}, "hello"); err != nil {
		t.Fatal(err)
	}
	if got.Signin != source || got.URL != "https://claude.ai/" {
		t.Fatalf("cookies=%+v", got)
	}
}

func TestCookieCacheIsKeyedByProfile(t *testing.T) {
	fixture := surfacetest.NewCookieFixture(t)
	a := browsercookie.Source{Bridge: fixture.Bridge, URL: "https://claude.ai/", Signin: signin.Source{ProfileID: "sp_a", Browser: signin.Chrome, Dir: "/a"}}
	b := browsercookie.Source{Bridge: fixture.Bridge, URL: "https://claude.ai/", Signin: signin.Source{ProfileID: "sp_b", Browser: signin.Chrome, Dir: "/b"}}
	for _, source := range []browsercookie.Source{a, a, b} {
		if header, err := browsercookie.LoadHeader(context.Background(), source); err != nil || header != "sid=fixture" {
			t.Fatalf("header=%q err=%v", header, err)
		}
	}
	if calls := surfacetest.ReadLines(t, fixture.BridgeLog); len(calls) != 2 {
		t.Fatalf("calls=%q, want one read per profile", calls)
	}
	browsercookie.Invalidate(a)
	if _, err := browsercookie.LoadHeader(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if calls := surfacetest.ReadLines(t, fixture.BridgeLog); len(calls) != 2 {
		t.Fatalf("invalidating one profile reread another: %q", calls)
	}
	if _, err := browsercookie.LoadHeader(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if calls := surfacetest.ReadLines(t, fixture.BridgeLog); len(calls) != 3 {
		t.Fatalf("calls=%q", calls)
	}
}
