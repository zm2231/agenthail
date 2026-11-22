package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestRemovingAProjectGitCannotReadKeepsItAndItsLead(t *testing.T) {
	for _, c := range []struct{ name, git, want string }{
		{"git stalls", "exec sleep 5", "git took longer than 200ms"},
		{"git refuses the repository", "echo 'fatal: detected dubious ownership in repository' >&2; exit 128", "dubious ownership"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, r, _, repo := projectsFixture(t)
			id := addedWithLead(t, d, repo)
			real, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = --show-toplevel ] && { " + c.git + "; }; done\nexec " + real + " \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			budget := projectAddGitTimeout
			projectAddGitTimeout = 200 * time.Millisecond
			t.Cleanup(func() { projectAddGitTimeout = budget })
			response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"`+id+`"}`)
			if response.Code == http.StatusOK || !strings.Contains(response.Body.String(), c.want) || !strings.Contains(response.Body.String(), "the project is still added") {
				t.Fatalf("the remove answered %d %s, want a refusal naming %q", response.Code, response.Body, c.want)
			}
			if lead, err := r.Coordinator(id); err != nil || lead.Name != "captain" {
				t.Fatalf("after the refused remove the project's lead is %+v (%v), want @captain still", lead, err)
			}
		})
	}
	t.Run("the folder is gone", func(t *testing.T) {
		d, _, _, repo := projectsFixture(t)
		id := addedWithLead(t, d, repo)
		if err := os.RemoveAll(repo); err != nil {
			t.Fatal(err)
		}
		if response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"`+id+`"}`); response.Code != http.StatusOK {
			t.Fatalf("removing a project whose folder is gone answered %d %s", response.Code, response.Body)
		}
	})
}

func addedWithLead(t *testing.T, d *Daemon, repo string) string {
	t.Helper()
	if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"captain"}`); response.Code != http.StatusOK {
		t.Fatalf("set: %d %s", response.Code, response.Body)
	}
	response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`)
	var added struct {
		Project struct{ ID string } `json:"project"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &added) != nil {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	return added.Project.ID
}
