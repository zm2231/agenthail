package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestMobileWorkspaceSurvivesSnapshotAndSearch(t *testing.T) {
	d, registry, fake, session, _ := daemonFixture(t)
	session.Cwd = "/work/projects/release"
	fake.sessions[session.ID] = session
	fake.searchResults = []surface.SessionSearchResult{{Session: session, Snippet: "release work"}}
	if err := registry.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	for _, path := range []string{"/api/v1/snapshot?fresh=1", "/api/v1/search?surface=codex&q=release"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		var body struct {
			Sessions []dashboardSession `json:"sessions"`
			Results  []struct {
				Session dashboardSession `json:"session"`
			} `json:"results"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, result := range body.Results {
			body.Sessions = append(body.Sessions, result.Session)
		}
		found := false
		for _, result := range body.Sessions {
			if result.ID == session.ID {
				found = true
				if result.Cwd != session.Cwd {
					t.Fatalf("%s lost workspace: %+v", path, result)
				}
			}
		}
		if !found {
			t.Fatalf("%s lost session", path)
		}
	}
}
