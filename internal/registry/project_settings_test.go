package registry

import (
	"testing"
	"time"
)

func TestProjectSettingsKeepTheLaterRecordWhicheverOrderTheyArrive(t *testing.T) {
	const project = "repo:github.com/example/app"
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	off := ProjectSetting{Project: project, Name: ProjectSettingMCP, Value: ProjectMCPOff, ChangedAt: at, OriginHost: "h-a"}
	on := ProjectSetting{Project: project, Name: ProjectSettingMCP, Value: ProjectMCPOn, ChangedAt: at.Add(time.Second), OriginHost: "h-b"}
	tie := ProjectSetting{Project: project, Name: ProjectSettingMCP, Value: ProjectMCPOff, ChangedAt: at.Add(time.Second), OriginHost: "h-a"}
	local := ProjectSetting{Project: "implicit:/home/me/notes", Name: ProjectSettingMCP, Value: ProjectMCPOff, ChangedAt: at, OriginHost: "h-a"}
	unknown := ProjectSetting{Project: project, Name: "colour", Value: "blue", ChangedAt: at.Add(time.Hour), OriginHost: "h-a"}
	for name, order := range map[string][]ProjectSetting{
		"off then on": {off, on, tie},
		"on then off": {tie, on, off},
	} {
		t.Run(name, func(t *testing.T) {
			r := openTestRegistry(t)
			for _, setting := range append(order, local, unknown) {
				if _, err := r.AdoptProjectSettings([]ProjectSetting{setting}); err != nil {
					t.Fatal(err)
				}
			}
			settings, err := r.ProjectSettings()
			if err != nil || len(settings) != 1 || settings[0].Value != ProjectMCPOn || settings[0].OriginHost != "h-b" {
				t.Fatalf("settings=%+v err=%v; want only the on from h-b, the later record", settings, err)
			}
			r.now = func() time.Time { return at }
			again, err := r.SetProjectSetting(project, ProjectSettingMCP, ProjectMCPOff)
			if err != nil || !again.ChangedAt.After(on.ChangedAt) {
				t.Fatalf("a setting made on a host whose clock is behind lost to the record it replaces: %+v err=%v", again, err)
			}
			if value, err := r.ProjectSettingValue(project, ProjectSettingMCP); err != nil || value != ProjectMCPOff {
				t.Fatalf("value = %q, %v", value, err)
			}
		})
	}
}

func TestAnUnsetProjectHasTheToolsAndOnlyKnownValuesAreTaken(t *testing.T) {
	r := openTestRegistry(t)
	if value, err := r.ProjectSettingValue("repo:github.com/example/app", ProjectSettingMCP); err != nil || value != ProjectMCPOn {
		t.Fatalf("an unset project reads %q, %v", value, err)
	}
	if _, err := r.SetProjectSetting("repo:github.com/example/app", ProjectSettingMCP, "maybe"); err == nil {
		t.Fatal("a value the setting does not have was taken")
	}
	if value, err := r.ProjectSettingValue("implicit:/home/me/notes", ProjectSettingMCP); err != nil || value != ProjectMCPOn {
		t.Fatalf("value = %q, %v", value, err)
	}
	_, before, err := r.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetProjectSetting("implicit:/home/me/notes", ProjectSettingMCP, ProjectMCPOff); err != nil {
		t.Fatal(err)
	}
	if _, after, err := r.CatalogState(); err != nil || after != before {
		t.Fatalf("a folder's setting reached the catalog stream paired hosts follow: seq %d -> %d, %v", before, after, err)
	}
	if value, err := r.ProjectSettingValue("implicit:/home/me/notes", ProjectSettingMCP); err != nil || value != ProjectMCPOff {
		t.Fatalf("a folder of this host's own did not keep its setting: %q, %v", value, err)
	}
}

func TestAMovedProjectKeepsItsSettingsUnderItsNewKey(t *testing.T) {
	const from, to = "repo:github.com/example/old", "repo:github.com/example/new"
	r := openTestRegistry(t)
	if _, err := r.SetProjectSetting(from, ProjectSettingMCP, ProjectMCPOff); err != nil {
		t.Fatal(err)
	}
	move := ProjectMove{From: from, To: to, ChangedAt: time.Now().Add(time.Minute).Truncate(time.Millisecond), OriginHost: "h-a"}
	if _, err := r.AdoptProjectMoves([]ProjectMove{move}); err != nil {
		t.Fatal(err)
	}
	if value, err := r.ProjectSettingValue(to, ProjectSettingMCP); err != nil || value != ProjectMCPOff {
		t.Fatalf("after the move the new key reads %q, %v", value, err)
	}
	stale := ProjectSetting{Project: from, Name: ProjectSettingMCP, Value: ProjectMCPOn, ChangedAt: move.ChangedAt.Add(time.Second), OriginHost: "h-b"}
	if _, err := r.AdoptProjectSettings([]ProjectSetting{stale}); err != nil {
		t.Fatal(err)
	}
	if value, err := r.ProjectSettingValue(to, ProjectSettingMCP); err != nil || value != ProjectMCPOn {
		t.Fatalf("a later record under the old key did not reach the moved project: %q, %v", value, err)
	}
}
