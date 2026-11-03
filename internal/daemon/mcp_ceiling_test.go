package daemon

import (
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAChildIsBoundedByItsSpawnersCeiling(t *testing.T) {
	bounded := sessionCeiling{Permission: "edits", Roots: []string{"/work/a"}}
	cases := []struct {
		name     string
		caller   sessionCeiling
		kind     surface.SurfaceKind
		mode     string
		cwd      string
		refusal  string
		axis     string
		wantMode string
		want     sessionCeiling
	}{
		{name: "a person's session starts anything, confined to the child's folder", caller: sessionCeiling{}, kind: surface.KindClaude, cwd: "/work/b", want: sessionCeiling{Roots: []string{"/work/b"}}},
		{name: "a requested mode becomes the child's limit", caller: sessionCeiling{}, kind: surface.KindClaude, mode: "acceptEdits", cwd: "/work/b", wantMode: "acceptEdits", want: sessionCeiling{Permission: "edits", Roots: []string{"/work/b"}}},
		{name: "omitted, the child gets the spawner's level", caller: bounded, kind: surface.KindClaude, cwd: "/work/a/sub", wantMode: "acceptEdits", want: bounded},
		{name: "a higher mode is refused", caller: bounded, kind: surface.KindClaude, mode: "bypassPermissions", cwd: "/work/a", refusal: "exceeds_ceiling", axis: "permission"},
		{name: "a folder outside the roots is refused", caller: bounded, kind: surface.KindClaude, cwd: "/work/ab", refusal: "exceeds_ceiling", axis: "roots"},
		{name: "a Codex child maps the level to an approval policy", caller: bounded, kind: surface.KindCodex, cwd: "/work/a", wantMode: "on-request", want: bounded},
		{name: "a Codex child cannot exceed it either", caller: bounded, kind: surface.KindCodex, mode: "never", cwd: "/work/a", refusal: "exceeds_ceiling", axis: "permission"},
		{name: "Codex has no plan level", caller: sessionCeiling{Permission: "plan"}, kind: surface.KindCodex, cwd: "/work/a", refusal: "exceeds_ceiling", axis: "permission"},
		{name: "an unknown mode is invalid", caller: sessionCeiling{}, kind: surface.KindClaude, mode: "yolo", cwd: "/work/a", refusal: "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, err := childCeiling(c.caller, c.kind, c.mode, c.cwd)
			if c.refusal != "" {
				refusal := mcpRefusalFor(err)
				if err == nil || refusal.Code != c.refusal || c.axis != "" && refusal.Detail["axis"] != c.axis {
					t.Fatalf("err=%v, want %s on %s", err, c.refusal, c.axis)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			mode := start.permissionMode + start.approval
			if mode != c.wantMode || start.ceiling.Permission != c.want.Permission || !slices.Equal(start.ceiling.Roots, c.want.Roots) {
				t.Fatalf("mode=%q ceiling=%+v, want %q %+v", mode, start.ceiling, c.wantMode, c.want)
			}
		})
	}
}

func TestABoundedSessionCannotControlOneThatMayDoMore(t *testing.T) {
	limit := sessionCeiling{Permission: "edits", Roots: []string{"/work"}}
	for _, target := range []sessionCeiling{{}, {Permission: "full", Roots: []string{"/work"}}, {Permission: "edits", Roots: []string{"/elsewhere"}}, {Permission: "edits"}} {
		if target.within(limit) {
			t.Fatalf("%+v counted as within %+v", target, limit)
		}
	}
	if !(sessionCeiling{Permission: "ask", Roots: []string{"/work/a"}}).within(limit) || !(sessionCeiling{}).within(sessionCeiling{}) {
		t.Fatal("a narrower ceiling was not within")
	}
}
