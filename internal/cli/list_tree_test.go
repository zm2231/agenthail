package cli

import (
	"reflect"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestListFamiliesNestsSubagentsAndCapsFamilies(t *testing.T) {
	sessions := []surface.Session{
		{ID: "child-ada", Name: "Ada task", Status: surface.StatusBusy, Subagent: &surface.Subagent{ParentID: "root", RootID: "root", Depth: 1, Nickname: "Ada"}},
		{ID: "other", Name: "Other root", Status: surface.StatusIdle},
		{ID: "grand-cy", Name: "Cy task", Status: surface.StatusBusy, Subagent: &surface.Subagent{ParentID: "child-ada", RootID: "root", Depth: 2, Nickname: "Cy"}},
		{ID: "root", Name: "Lead", Status: surface.StatusIdle},
		{ID: "orphan", Name: "Orphan task", Status: surface.StatusIdle, Subagent: &surface.Subagent{ParentID: "gone", RootID: "gone", Depth: 1, Nickname: "Dee"}},
		{ID: "claude", Name: "Claude lead", Status: surface.StatusBusy, Subagents: &surface.SubagentRollup{Count: 4, Working: 2}},
	}
	rows := listFamilies(sessions, -1)
	var got []string
	for _, row := range rows {
		got = append(got, listRowName(row))
	}
	if want := []string{"Other root", "Lead", "Ada", "Cy", "Orphan task", "Claude lead"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	if rows[3].depth != 2 || rows[2].depth != 1 || rows[1].depth != 0 {
		t.Fatalf("depths = %+v", rows)
	}
	capped := listFamilies(sessions, 2)
	if len(capped) != 4 || capped[3].session.ID != "grand-cy" {
		t.Fatalf("a family cap keeps whole families: %+v", capped)
	}

	rollups := listRollups(sessions)
	if rollups["root"] != (surface.SubagentRollup{Count: 2, Working: 2}) || rollups["child-ada"] != (surface.SubagentRollup{Count: 1, Working: 1}) {
		t.Fatalf("codex rollups = %+v", rollups)
	}
	if rollups["claude"] != (surface.SubagentRollup{Count: 4, Working: 2}) {
		t.Fatalf("claude rollup = %+v", rollups["claude"])
	}
	if _, found := rollups["other"]; found {
		t.Fatalf("a session without subagents has a rollup: %+v", rollups["other"])
	}
}
