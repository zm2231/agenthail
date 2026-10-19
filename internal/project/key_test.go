package project

import "testing"

func TestNormalizeRemoteMatchesEveryFormOfOneRepository(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/example/gadget.git",
		"https://user@GitHub.com/example/gadget/",
		"ssh://git@github.com:22/example/gadget.git",
		"git@github.com:example/gadget",
		"github.com:example/gadget.git",
	} {
		if got, ok := NormalizeRemote(raw); !ok || got != "github.com/example/gadget" {
			t.Errorf("NormalizeRemote(%q) = %q, %t", raw, got, ok)
		}
	}
	for _, raw := range []string{"", "/srv/git/gadget.git", "../gadget", "file:///srv/git/gadget.git"} {
		if got, ok := NormalizeRemote(raw); ok {
			t.Errorf("NormalizeRemote(%q) = %q, want a local path to be rejected", raw, got)
		}
	}
}
