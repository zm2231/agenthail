package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestMobileCreationOptionsAndAuthorizedCreation(t *testing.T) {
	d, registry, fake, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	for _, authorized := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session-options", nil)
		if authorized {
			request.Header.Set("Authorization", "Bearer secret")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !authorized {
			if response.Code != http.StatusUnauthorized {
				t.Fatal(response.Code)
			}
			continue
		}
		var options struct {
			Surfaces []struct {
				ID        string `json:"id"`
				Workspace bool   `json:"workspace"`
			} `json:"surfaces"`
			Workspaces []string `json:"workspaces"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &options); err != nil {
			t.Fatal(err)
		}
		if len(options.Surfaces) == 0 {
			t.Fatal("missing starters")
		}
	}
	for _, authorized := range []bool{false, true} {
		body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "codex", "message": "Build", "cwd": t.TempDir()})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
		if authorized {
			request.Header.Set("Authorization", "Bearer secret")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !authorized {
			if response.Code != http.StatusUnauthorized || len(fake.startOptions) != 0 {
				t.Fatal(response.Code)
			}
			continue
		}
		if response.Code != http.StatusCreated || len(fake.startOptions) != 1 {
			t.Fatal(response.Code, response.Body.String())
		}
		if _, err := registry.Session("started"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMobileQueueIncludesExpiredAndRetriesThroughActionAPI(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	id, err := registry.QueueMessageWithKey("from", "Expired instruction", "expiry-probe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ClaimNextMessage("from", time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []dashboardQueue `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.ID == id && item.Status == "expired" {
			if !item.Historical || item.Evidence != surface.EvidenceExpired {
				t.Fatalf("expired row=%+v", item)
			}
			found = true
		}
	}
	if !found {
		t.Fatal(response.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"action": "queue-retry", "queueId": id})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	item, err := registry.QueueItem(id)
	if err != nil || item.Status != "pending" {
		t.Fatalf("%+v %v", item, err)
	}
}
