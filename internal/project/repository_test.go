package project

import "testing"

func TestCanonicalKeyMatchesEverySpellingOfOneRepository(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/Example/Gadget.git",
		"https://user@github.com/example/gadget/",
		"ssh://git@github.com:22/example/gadget.git",
		"git@github.com:example/gadget",
		"github.com:example/gadget.git",
	} {
		if got := CanonicalKey(raw); got != "github.com/example/gadget" {
			t.Errorf("CanonicalKey(%q) = %q", raw, got)
		}
	}
	if got := CanonicalKey("git@ssh.dev.azure.com:v3/org/proj/repo"); got != "dev.azure.com/org/proj/_git/repo" {
		t.Errorf("azure ssh remote = %q", got)
	}
}

func TestPickRemotePrefersOriginAndSkipsLocalRemotes(t *testing.T) {
	picked, ok := PickRemote(map[string]string{"upstream": "https://github.com/tailscale/setec", "origin": "git@github.com:example/setec.git"})
	if !ok || picked.RemoteName != "origin" || picked.CanonicalKey != "github.com/example/setec" || picked.Name != "setec" {
		t.Fatalf("picked=%+v ok=%t", picked, ok)
	}
	picked, ok = PickRemote(map[string]string{"zeta": "https://github.com/example/z", "alpha": "https://github.com/example/a", "origin": "../local"})
	if !ok || picked.RemoteName != "alpha" {
		t.Fatalf("picked=%+v ok=%t", picked, ok)
	}
	for _, local := range []string{"/srv/git/gadget.git", "../gadget", ".", "file:///srv/git/gadget.git", `C:\repos\gadget`} {
		if picked, ok := PickRemote(map[string]string{"origin": local}); ok {
			t.Errorf("local remote %q was picked: %+v", local, picked)
		}
	}
}

func TestKeyAddsTheFolderInsideTheRepository(t *testing.T) {
	if got := Key("github.com/example/gadget", ""); got != "repo:github.com/example/gadget" {
		t.Errorf("root key = %q", got)
	}
	if got := Key("github.com/example/gadget", "apps/web/"); got != "repo:github.com/example/gadget::apps/web" {
		t.Errorf("subfolder key = %q", got)
	}
}
