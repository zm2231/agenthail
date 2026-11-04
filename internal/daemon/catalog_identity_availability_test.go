package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func failingGitStatus(t *testing.T) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = status ]; then echo 'fatal: index file corrupt' >&2; exit 128; fi\ndone\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func snapshotSession(t *testing.T, handler http.Handler, id string) dashboardSession {
	t.Helper()
	response := getSnapshot(t, handler, "/api/v1/snapshot?fresh=1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", response.Code, response.Body)
	}
	var snapshot struct {
		Sessions []dashboardSession `json:"sessions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, row := range snapshot.Sessions {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("session %s is not in the snapshot", id)
	return dashboardSession{}
}

func TestASessionWhoseRepositoryStatusFailsStaysAvailableAndSendable(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	repo := catalogTestRepo(t)
	from.Cwd = repo
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	fake.sessions[from.ID] = from
	failingGitStatus(t)
	d.discoverCatalog(context.Background())
	handler := d.dashboardHandler(&dashboardServer{token: dashboardTestToken})

	row := snapshotSession(t, handler, from.ID)
	if row.UnavailableReason != "" || row.Display == nil || row.Display.Kind != "idle" {
		t.Fatalf("a failed repository status made the session unavailable: reason=%q display=%+v", row.UnavailableReason, row.Display)
	}
	if row.Project == nil || row.Checkout == nil || row.Checkout.Branch != "main" {
		t.Fatalf("the workspace identity git could read was dropped: project=%+v checkout=%+v", row.Project, row.Checkout)
	}
	if response := postAction(t, handler, map[string]any{"action": "send", "sessionId": from.ID, "message": "still here"}); response.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body)
	}
	if len(fake.sent) != 1 || !strings.HasSuffix(fake.sent[0], "still here") {
		t.Fatalf("sent=%v", fake.sent)
	}
}

func TestASessionWithoutAWorkspaceIsNotUnavailable(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	row := snapshotSession(t, d.dashboardHandler(&dashboardServer{token: dashboardTestToken}), from.ID)
	if row.UnavailableReason != "" || row.Display == nil || row.Display.Kind != "idle" {
		t.Fatalf("reason=%q display=%+v", row.UnavailableReason, row.Display)
	}
}
