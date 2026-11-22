package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAddingARepositoryWithoutARemoteKeepsItsLeadAndRemovingItGivesTheLeadBack(t *testing.T) {
	d, r, _, repo := projectsFixture(t)
	if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"captain"}`); response.Code != http.StatusOK {
		t.Fatalf("set: %d %s", response.Code, response.Body)
	}
	before, err := r.CoordinatorNamed("captain")
	if err != nil || !strings.HasPrefix(before.ProjectID, "implicit:") {
		t.Fatalf("the lead before the add is %+v (%v), want it on the repository's implicit project", before, err)
	}
	response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	var added struct {
		Project struct{ ID string } `json:"project"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &added); err != nil || !strings.HasPrefix(added.Project.ID, "p-") {
		t.Fatalf("add answered %s (%v)", response.Body, err)
	}
	if lead, err := r.Coordinator(added.Project.ID); err != nil || lead.Name != "captain" || lead.SessionID != before.SessionID {
		t.Fatalf("the added project's lead is %+v (%v), want @captain", lead, err)
	}
	if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"captain","replace":true}`); response.Code != http.StatusOK {
		t.Fatalf("setting the lead's own name again: %d %s", response.Code, response.Body)
	}
	if response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"`+added.Project.ID+`"}`); response.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", response.Code, response.Body)
	}
	if lead, err := r.Coordinator(before.ProjectID); err != nil || lead.Name != "captain" {
		t.Fatalf("after the remove the implicit project's lead is %+v (%v), want @captain back", lead, err)
	}
}
