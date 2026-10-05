package registry

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func registerFamily(t *testing.T, r *Registry) {
	t.Helper()
	sessions := []surface.Session{
		{ID: "root-thread", Surface: surface.KindCodex, Name: "Build the parser", Cwd: "/repo/app"},
		{ID: "child-ada", Surface: surface.KindCodex, Name: "Build the parser tests", Cwd: "/repo/app", Subagent: &surface.Subagent{ParentID: "root-thread", RootID: "root-thread", Depth: 1, Nickname: "Ada"}},
		{ID: "child-bo", Surface: surface.KindCodex, Name: "Review", Cwd: "/repo/app", Subagent: &surface.Subagent{ParentID: "root-thread", RootID: "root-thread", Depth: 1, Nickname: "Bo", Role: "reviewer"}},
		{ID: "child-bo-2", Surface: surface.KindCodex, Name: "Review again", Cwd: "/repo/app", Subagent: &surface.Subagent{ParentID: "root-thread", RootID: "root-thread", Depth: 1, Nickname: "bo"}},
		{ID: "grandchild-cy", Surface: surface.KindCodex, Name: "Leaf", Cwd: "/repo/app", Subagent: &surface.Subagent{ParentID: "child-ada", RootID: "root-thread", Depth: 2, Nickname: "Cy"}},
	}
	for _, session := range sessions {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.SetAlias("lead", "root-thread"); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryPersistsSubagentIdentity(t *testing.T) {
	r := openTestRegistry(t)
	registerFamily(t, r)
	got, err := r.Session("grandchild-cy")
	if err != nil {
		t.Fatal(err)
	}
	want := &surface.Subagent{ParentID: "child-ada", RootID: "root-thread", Depth: 2, Nickname: "Cy"}
	if !reflect.DeepEqual(got.Subagent, want) {
		t.Fatalf("subagent = %+v, want %+v", got.Subagent, want)
	}
	if err := r.RegisterSession(surface.Session{ID: "grandchild-cy", Surface: surface.KindCodex, Name: "Leaf"}); err != nil {
		t.Fatal(err)
	}
	got, err = r.Session("grandchild-cy")
	if err != nil || !reflect.DeepEqual(got.Subagent, want) {
		t.Fatalf("a registration without subagent identity keeps it: %+v, %v", got.Subagent, err)
	}
	listed, err := r.ListSessions(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range listed {
		if session.ID == "root-thread" && session.Subagent != nil {
			t.Fatalf("root has subagent identity %+v", session.Subagent)
		}
		if session.ID == "child-bo" && (session.Subagent == nil || session.Subagent.Role != "reviewer") {
			t.Fatalf("listed child = %+v", session.Subagent)
		}
	}
}

func TestResolveTargetSendsFamilyFragmentsToTheRoot(t *testing.T) {
	r := openTestRegistry(t)
	registerFamily(t, r)
	for _, target := range []string{"Build the parser", "/repo/app", "lead", "@lead"} {
		got, err := r.ResolveTarget(target)
		if err != nil || got != "root-thread" {
			t.Fatalf("%q resolved to %q, %v; want the family root", target, got, err)
		}
	}
	if got, err := r.ResolveTarget("child-bo-2"); err != nil || got != "child-bo-2" {
		t.Fatalf("a subagent stays addressable by its own ID: %q, %v", got, err)
	}
}

func TestResolveTargetWalksSubagentPaths(t *testing.T) {
	r := openTestRegistry(t)
	registerFamily(t, r)
	for target, want := range map[string]string{
		"@lead/ada":                  "child-ada",
		"@lead/ADA/cy":               "grandchild-cy",
		"@root-thread/child-bo-2":    "child-bo-2",
		"@Build the parser/Ada/Cy":   "grandchild-cy",
		"@lead/grandchild":           "",
		"@lead/nobody":               "",
		"@lead/bo":                   "",
		"@lead/":                     "",
		"@missing/ada":               "",
		"@root-thread/child-a":       "child-ada",
		"@child-ada/grandchild-cy/x": "",
		"@lead/child-bo":             "child-bo",
	} {
		got, err := r.ResolveTarget(target)
		if want == "" {
			if err == nil {
				t.Fatalf("%q resolved to %q; want an error", target, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Fatalf("%q resolved to %q, %v; want %q", target, got, err, want)
		}
	}
	_, err := r.ResolveTarget("@lead/bo")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "child-bo, child-bo-2") {
		t.Fatalf("a nickname shared by two siblings is ambiguous: %v", err)
	}
}
