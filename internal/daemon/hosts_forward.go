package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/uploads"
)

func writeHostError(w http.ResponseWriter, host *pairedHost, err error) {
	var notSent hosts.NotSentError
	if errors.As(err, &notSent) {
		writeAPIError(w, http.StatusServiceUnavailable, "host_unreachable", fmt.Sprintf("Host %s is unreachable: %s", host.record.Name, err))
		return
	}
	writeAPIError(w, http.StatusGatewayTimeout, "host_outcome_unknown", fmt.Sprintf("Host %s did not answer; the outcome is unknown: %s", host.record.Name, err))
}

func writeHostReadError(w http.ResponseWriter, host *pairedHost, err error) {
	writeAPIError(w, http.StatusServiceUnavailable, "host_unreachable", fmt.Sprintf("Host %s is unreachable: %s", host.record.Name, err))
}

const maxForwardedBodyBytes = uploads.MaxFileBytes + 1

func (d *Daemon) forwardHostRequest(w http.ResponseWriter, r *http.Request, host *pairedHost, method string, query url.Values, body []byte) {
	header := http.Header{}
	if key := r.Header.Get(actionIdempotencyHeader); key != "" {
		header.Set(actionIdempotencyHeader, key)
	}
	if contentType := r.Header.Get("Content-Type"); body != nil && contentType != "" {
		header.Set("Content-Type", contentType)
	}
	response, err := host.client.Do(r.Context(), method, apiPath(r.URL.Path), query, body, header)
	if err != nil {
		writeHostReadError(w, host, err)
		return
	}
	writeHostResponse(w, host, response)
}

func writeHostResponse(w http.ResponseWriter, host *pairedHost, response *http.Response) {
	answer, err := readHostAnswer(host, response)
	if err != nil {
		writeHostError(w, host, err)
		return
	}
	answer.write(w)
}

type hostAnswer struct {
	status  int
	header  http.Header
	payload []byte
}

func readHostAnswer(host *pairedHost, response *http.Response) (hostAnswer, error) {
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return hostAnswer{}, err
	}
	contentType := response.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") && len(bytes.TrimSpace(payload)) > 0 {
		if qualified, qualifyErr := hosts.QualifyJSON(host.record.ID, payload); qualifyErr == nil {
			payload = qualified
		}
	}
	header := http.Header{}
	for _, key := range []string{"Content-Type", "Content-Disposition", "Cache-Control"} {
		if value := response.Header.Get(key); value != "" {
			header.Set(key, value)
		}
	}
	return hostAnswer{status: response.StatusCode, header: header, payload: payload}, nil
}

func (a hostAnswer) write(w http.ResponseWriter) {
	for key, values := range a.header {
		w.Header()[key] = values
	}
	w.WriteHeader(a.status)
	_, _ = w.Write(a.payload)
}
