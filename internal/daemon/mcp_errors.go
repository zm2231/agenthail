package daemon

import (
	"errors"
	"net/http"
	"slices"
	"strings"
)

func mcpRefusalFor(err error) *mcpRefusal {
	var refusal *mcpRefusal
	if errors.As(err, &refusal) {
		return refusal
	}
	var failure *actionError
	if !errors.As(err, &failure) {
		return &mcpRefusal{Code: "refused", Message: err.Error(), Retryable: true}
	}
	mapped, _ := mcpCodeFor(failure.Status, failure.Code)
	mapped.Message = failure.Message
	if mapped.Detail == nil && len(failure.Extra) > 0 {
		mapped.Detail = map[string]any{}
	}
	for key, value := range failure.Extra {
		mapped.Detail[key] = value
	}
	return &mapped
}

var refusedConflicts = []string{"conflict", "daemon_session", "coordinator_exists", "no_lead", "fork_turn_unknown", "not_replaced", "project_history", "project_linked", "project_not_linked"}

func mcpCodeFor(status int, code string) (mcpRefusal, bool) {
	switch {
	case code == "ledger_owner_unreachable":
		return mcpRefusal{Code: "owner_unreachable", Retryable: true}, true
	case status == http.StatusBadRequest:
		return mcpRefusal{Code: "invalid"}, true
	case status == http.StatusNotFound:
		return mcpRefusal{Code: "not_found"}, true
	case status == http.StatusConflict && code == "stale":
		return mcpRefusal{Code: "not_found", Detail: map[string]any{"stale": true}}, true
	case status == http.StatusConflict && (code == "move_busy" || code == "session_adopt_busy"):
		return mcpRefusal{Code: "busy", Retryable: true}, true
	case status == http.StatusConflict && (slices.Contains(refusedConflicts, code) || strings.HasSuffix(code, "_refused") || strings.HasSuffix(code, "_needs_consent")):
		return mcpRefusal{Code: "refused"}, true
	case status == http.StatusBadGateway && code == "control_failed":
		return mcpRefusal{Code: "busy", Retryable: true}, true
	case status == http.StatusBadGateway && strings.HasSuffix(code, "_unknown"):
		return mcpRefusal{Code: "busy", Detail: map[string]any{"outcome": "unknown"}}, true
	case status == http.StatusBadGateway && (code == "bad_gateway" || strings.HasSuffix(code, "_failed")):
		return mcpRefusal{Code: "refused", Retryable: true}, true
	case status == http.StatusInternalServerError:
		return mcpRefusal{Code: "refused", Retryable: true}, true
	}
	return mcpRefusal{Code: "refused"}, false
}
