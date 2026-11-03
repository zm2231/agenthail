package daemon

import (
	"errors"
	"net/http"
)

type actionResult struct {
	Status int
	Body   any
}

type actionError struct {
	Status  int
	Code    string
	Message string
	Extra   map[string]any
}

func (e *actionError) Error() string { return e.Message }

func actionFailed(status int, code, message string) error {
	return &actionError{Status: status, Code: code, Message: message}
}

func actionOK(body any) actionResult {
	return actionResult{Status: http.StatusOK, Body: body}
}

func writeActionResult(w http.ResponseWriter, result actionResult, err error) {
	var failure *actionError
	if errors.As(err, &failure) {
		writeAPIErrorWith(w, failure.Status, failure.Code, failure.Message, failure.Extra)
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeDashboardJSON(w, result.Status, result.Body)
}
