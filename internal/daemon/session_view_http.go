package daemon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

var errPresentationChanged = errors.New("the catalog changed; reload the view")
var errPresentationMember = errors.New("session is outside the selected view")
var errPresentationCursor = errors.New("invalid catalog view cursor")

type sessionViewRequest struct {
	Detail   *dashboardSession
	Scope    string
	Filters  SessionViewFilters
	Limit    int
	Offset   int
	Revision string
	IDs      []string
}

type sessionViewCursor struct {
	Scope    string
	Filters  SessionViewFilters
	Revision string
	Offset   int
}

func encodeSessionViewCursor(request sessionViewRequest, revision string, offset int) string {
	encoded, _ := json.Marshal(sessionViewCursor{request.Scope, request.Filters, revision, offset})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func parseSessionViewRequest(r *http.Request) (sessionViewRequest, error) {
	q := r.URL.Query()
	request := sessionViewRequest{Scope: q.Get("scope"), Limit: 50, Revision: q.Get("revision"), IDs: q["sessionId"], Filters: SessionViewFilters{ProjectID: q.Get("projectId"), Query: strings.ToLower(strings.TrimSpace(q.Get("q"))), Surface: q.Get("surface"), Status: q.Get("status"), Attention: q.Get("attention") == "1", SelectedID: q.Get("selectedId")}}
	if request.Scope == "" || !validCatalogScope(request.Scope) {
		return request, fmt.Errorf("use running, current, recent, all, or archived")
	}
	if q.Get("parentId") != "" {
		return request, fmt.Errorf("parentId pages cannot select a scope")
	}
	if len(request.Filters.Query) > 1000 || len(request.IDs) > 200 {
		return request, fmt.Errorf("view request is too large")
	}
	if q.Has("attention") && q.Get("attention") != "1" {
		return request, fmt.Errorf("attention must be 1")
	}
	if request.Filters.Status != "" {
		valid := false
		for _, kind := range []string{"unavailable", "waiting", "working", "limited", "finishing", "queued", "offline", "idle"} {
			valid = valid || request.Filters.Status == kind
		}
		if !valid {
			return request, fmt.Errorf("invalid display status")
		}
	}
	if q.Has("limit") {
		limit, err := strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 200 {
			return request, fmt.Errorf("use a limit between 1 and 200")
		}
		request.Limit = limit
	}
	if q.Has("cursor") {
		var cursor sessionViewCursor
		encoded, err := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		if err != nil || json.Unmarshal(encoded, &cursor) != nil || cursor.Scope != request.Scope || cursor.Filters != request.Filters || cursor.Offset < 0 || cursor.Revision == "" {
			return request, errPresentationCursor
		}
		if request.Revision != "" && request.Revision != cursor.Revision {
			return request, errPresentationCursor
		}
		request.Revision, request.Offset = cursor.Revision, cursor.Offset
	}
	if len(request.IDs) > 0 && (request.Revision == "" || q.Has("cursor")) {
		return request, fmt.Errorf("member hydration requires a revision and no cursor")
	}
	return request, nil
}

func (d *Daemon) sessionViewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	request, err := parseSessionViewRequest(r)
	if err != nil {
		code := "invalid_view"
		if errors.Is(err, errPresentationCursor) {
			code = "invalid_cursor"
		}
		writeAPIError(w, http.StatusBadRequest, code, err.Error())
		return
	}
	state, err := d.readSessionView(r.Context(), request)
	if err != nil {
		status, code := http.StatusInternalServerError, "internal_error"
		if errors.Is(err, errPresentationChanged) {
			status, code = http.StatusConflict, "catalog_changed"
		}
		if errors.Is(err, errPresentationCursor) {
			status, code = http.StatusBadRequest, "invalid_cursor"
		}
		if errors.Is(err, errPresentationMember) {
			status, code = http.StatusBadRequest, "invalid_member"
		}
		writeAPIError(w, status, code, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	tag := fmt.Sprintf(`"%s-%d"`, state.View.Revision, request.Limit)
	if request.Offset == 0 && len(request.IDs) == 0 {
		w.Header().Set("ETag", tag)
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	writeDashboardJSON(w, http.StatusOK, state)
}
