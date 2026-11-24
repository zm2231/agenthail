package cli

import (
	"fmt"
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/agentroles"
)

func TestRolesLedgersNamesProjectsByNameAndStoresTheirIDs(t *testing.T) {
	ids := map[string]string{"alpha": "repo:github.com/acme/alpha", "beta": "repo:github.com/acme/beta"}
	resolve := func(name string) (string, error) {
		if id, found := ids[name]; found {
			return id, nil
		}
		return "", fmt.Errorf("no project is named %q", name)
	}
	worker := agentroles.BuiltIn[agentroles.Worker]
	remote := agentroles.BuiltIn[agentroles.Remote]

	role, err := editLedgers(worker, []string{"+alpha", "+beta", "-alpha"}, resolve)
	if err != nil || !slices.Equal(role.Ledgers, []string{ids["beta"]}) {
		t.Fatalf("+alpha +beta -alpha = %v, %v", role.Ledgers, err)
	}
	if _, err := editLedgers(worker, []string{"+gamma"}, resolve); err == nil {
		t.Fatal("adding an unknown project was accepted")
	}
	stale := agentroles.Role{Reach: agentroles.ReachDescendants, Ledgers: []string{"repo:github.com/acme/gone"}}
	if role, err := editLedgers(stale, []string{"-repo:github.com/acme/gone"}, resolve); err != nil || len(role.Ledgers) != 0 || role.Ledgers == nil {
		t.Fatalf("removing a project that no longer resolves = %v, %v", role.Ledgers, err)
	}
	if _, err := editLedgers(remote, []string{"+alpha"}, resolve); err == nil {
		t.Fatal("adding to a role that reaches every ledger was accepted")
	}
	if role, err := editLedgers(remote, []string{"own"}, resolve); err != nil || role.AllLedgers() || role.ReachesLedger(ids["alpha"]) {
		t.Fatalf("own on the remote role = %v, %v", role.Ledgers, err)
	}
	if role, err := editLedgers(worker, []string{"all"}, resolve); err != nil || !role.ReachesLedger(ids["alpha"]) {
		t.Fatalf("all on the worker role = %v, %v", role.Ledgers, err)
	}
	if role, err := editLedgers(agentroles.Role{Reach: agentroles.ReachAll, Ledgers: []string{}}, []string{"default"}, resolve); err != nil || !role.AllLedgers() {
		t.Fatalf("default on a reach-all role = %v, %v", role.Ledgers, err)
	}
}

func TestRolesToolsChangesTheToolsTheRoleSeesNow(t *testing.T) {
	every := []string{"agenthail_queue", "agenthail_send", "agenthail_sessions", "agenthail_whoami"}
	seen := []string{"agenthail_send", "agenthail_sessions", "agenthail_whoami"}
	worker := agentroles.BuiltIn[agentroles.Worker]
	if role, err := editRole(worker, "tools", []string{"-agenthail_sessions"}, every, seen); err != nil || !slices.Equal(role.Tools, []string{"agenthail_send", "agenthail_whoami"}) {
		t.Fatalf("taking one tool from a default role = %v, %v", role.Tools, err)
	}
	if role, err := editRole(worker, "tools", []string{"all"}, every, seen); err != nil || !slices.Equal(role.Tools, every) || role.Normalized().Tools == nil {
		t.Fatalf("all = %v, %v", role.Tools, err)
	}
	if role, err := editRole(agentroles.Role{Reach: agentroles.ReachDescendants, Tools: []string{"agenthail_send"}}, "tools", []string{"default"}, every, seen); err != nil || role.Tools != nil {
		t.Fatalf("default = %v, %v", role.Tools, err)
	}
}
