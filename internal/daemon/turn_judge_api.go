package daemon

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) turnJudgmentsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for turn judgments.")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "limit must be 1 to 1000.")
			return
		}
		limit = parsed
	}
	judgments, err := d.Registry.TurnJudgments(strings.TrimSpace(r.URL.Query().Get("sessionId")), limit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Turn judgments are unavailable.")
		return
	}
	if judgments == nil {
		judgments = []registry.TurnJudgment{}
	}
	writeDashboardJSON(w, http.StatusOK, turnJudgmentsPage{Judge: d.turnJudgeHealth(), Judgments: judgments})
}

type turnJudgmentsPage struct {
	Judge     *turnJudgeHealth        `json:"judge"`
	Judgments []registry.TurnJudgment `json:"judgments"`
}
