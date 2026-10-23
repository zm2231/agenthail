package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheMacSetsThePersonsNameAndAPhoneOnlyReadsIt(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	t.Setenv("HOME", t.TempDir())
	_, phone, err := d.Registry.CompleteDevicePairing(mustCreatePairing(t, d), "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	handler := dashboardRouter(d)
	call := func(token, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	person := func(token string) dashboardPerson {
		t.Helper()
		response := call(token, http.MethodGet, "/api/v1/snapshot?fresh=1", "")
		var state struct {
			Person dashboardPerson `json:"person"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
			t.Fatalf("snapshot status=%d body=%s", response.Code, response.Body.String())
		}
		return state.Person
	}

	account := person(phone)
	if account.Set || account.Name == "" {
		t.Fatalf("before any name is set: %+v", account)
	}
	if response := call("secret", http.MethodPost, "/api/v1/settings", `{"action":"person-name","personName":"  Ada  "}`); response.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", response.Code, response.Body.String())
	}
	if got := person(phone); got != (dashboardPerson{Name: "Ada", Set: true}) {
		t.Fatalf("a phone reads %+v", got)
	}
	if response := call(phone, http.MethodPost, "/api/v1/settings", `{"action":"person-name","personName":"Mallory"}`); response.Code != http.StatusForbidden && response.Code != http.StatusUnauthorized {
		t.Fatalf("a phone set the name: status=%d body=%s", response.Code, response.Body.String())
	}
	if got := person("secret"); got.Name != "Ada" {
		t.Fatalf("after the phone's attempt: %+v", got)
	}
	if response := call("secret", http.MethodPost, "/api/v1/settings", `{"action":"person-name","personName":""}`); response.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", response.Code, response.Body.String())
	}
	if got := person(phone); got != account {
		t.Fatalf("after reset %+v, want the account's %+v", got, account)
	}
}
