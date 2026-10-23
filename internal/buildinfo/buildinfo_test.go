package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestAStampedBuildKeepsWhatItWasStampedWith(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.1"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "ffff"}, {Key: "vcs.time", Value: "2026-01-01T00:00:00Z"}}}
	got := resolve("v0.2.68", "e387b0d87b52f55754d786bc909ab5346731f200", "2026-10-23T11:33:26Z", info)
	want := Identity{Version: "v0.2.68", Revision: "e387b0d87b52f55754d786bc909ab5346731f200", BuiltAt: "2026-10-23T11:33:26Z"}
	if got != want {
		t.Fatalf("identity=%+v, want %+v", got, want)
	}
	if got.String() != "v0.2.68 (e387b0d8)" {
		t.Fatalf("string=%q", got.String())
	}
}

func TestAnUnstampedBuildReadsTheToolchainVCSData(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef0123456789"}, {Key: "vcs.time", Value: "2026-10-23T10:00:00Z"}, {Key: "vcs.modified", Value: "true"}}}
	got := resolve("", "", "", info)
	if got.Version != "dev" || got.Revision != "abcdef0123456789" || !got.Modified || got.BuiltAt != "2026-10-23T10:00:00Z" {
		t.Fatalf("identity=%+v", got)
	}
	if got.String() != "dev (abcdef01, modified)" {
		t.Fatalf("string=%q", got.String())
	}
	if bare := resolve("", "", "", nil); bare.Version != "dev" || bare.RevisionKnown() {
		t.Fatalf("a build with no data=%+v", bare)
	}
}

func TestBuildsAreComparedOnlyWhenBothNameTheirCommit(t *testing.T) {
	release := Identity{Version: "v0.2.68", Revision: "e387b0d8"}
	next := Identity{Version: "v0.2.69", Revision: "abcd1234"}
	for _, test := range []struct {
		name        string
		a, b        Identity
		same, known bool
	}{
		{"one commit", release, release, true, true},
		{"two commits", release, next, false, true},
		{"an unknown revision", release, Identity{Version: "dev", Revision: "unknown"}, false, false},
		{"a modified tree", release, Identity{Version: "dev", Revision: "e387b0d8", Modified: true}, false, false},
	} {
		if same, known := SameBuild(test.a, test.b); same != test.same || known != test.known {
			t.Fatalf("%s: same=%v known=%v", test.name, same, known)
		}
	}
}
