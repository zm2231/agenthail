package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) mcpReadRemote(ctx context.Context, qualifiedID string, input mcpReadInput) (any, error) {
	switch {
	case input.Since != nil:
		return nil, refuse("invalid", "since reads only sessions on this computer; read a session on another computer back without since.")
	case input.BodyRef != "":
		return d.mcpReadRemoteBody(ctx, qualifiedID, input.BodyRef, input.Offset, input.Version)
	}
	limit, err := pageLimit(input.Limit, 5, surface.MaxExchangePage)
	if err != nil {
		return nil, err
	}
	before, after, err := decodeReadCursor(input.Before)
	if err != nil {
		return nil, err
	}
	query := url.Values{"timeline": {"1"}, "wait": {"1"}, "limit": {strconv.Itoa(limit)}}
	if before > 0 {
		query.Set("timelineBefore", strconv.FormatInt(before, 10))
	}
	var answer struct {
		JournalSeq uint64                   `json:"journalSeq"`
		Timeline   *surface.SessionTimeline `json:"timeline"`
	}
	var failure *hostAnswerError
	if err := d.hostGet(ctx, qualifiedID, "/api/v1/session", "id", query, &answer); errors.As(err, &failure) && failure.code == "history_gap" {
		return mcpNoOlderHistory(qualifiedID), nil
	} else if err != nil {
		return nil, err
	}
	if answer.Timeline == nil {
		return nil, &mcpRefusal{Code: "busy", Message: "The session's host answered without its history.", Retryable: true}
	}
	page := &surface.SessionReadResult{JournalSeq: answer.JournalSeq, Items: answer.Timeline.Items, NextBefore: answer.Timeline.NextBefore, Source: answer.Timeline.Source, Truncated: answer.Timeline.Truncated, UnavailableReason: answer.Timeline.UnavailableReason}
	return mcpReadPage(qualifiedID, page, before, after)
}

func (d *Daemon) mcpReadRemoteBody(ctx context.Context, qualifiedID, ref string, offset int, version *uint64) (any, error) {
	if offset < 0 {
		return nil, refuse("invalid", "offset must not be negative.")
	}
	query := url.Values{"ref": {ref}, "start": {strconv.Itoa(offset)}, "end": {strconv.Itoa(offset + mcpBodyChunk)}}
	if version != nil {
		query.Set("version", strconv.FormatUint(*version, 10))
	}
	var answer struct {
		Version *uint64 `json:"version"`
		Total   int     `json:"total"`
		Body    string  `json:"body"`
	}
	var failure *hostAnswerError
	if err := d.hostGet(ctx, qualifiedID, "/api/v1/session-stream-body", "id", query, &answer); errors.As(err, &failure) && failure.code == "body_unavailable" {
		return nil, refuse("not_found", "That body is no longer retained.")
	} else if errors.As(err, &failure) && failure.code == "invalid_range" {
		return nil, refuse("invalid", "The body range is invalid.")
	} else if errors.As(err, &failure) && failure.code == bodyRewrittenCode {
		return nil, errMCPBodyRewritten
	} else if err != nil {
		return nil, err
	}
	if version != nil && answer.Version == nil {
		return nil, refuse("refused", "The session's host runs an older Agenthail that cannot check a body version; read without version, or update that host.")
	}
	if version != nil && *answer.Version != *version {
		return nil, errMCPBodyRewritten
	}
	return mcpBodyPage(qualifiedID, ref, offset, []byte(answer.Body), answer.Total, answer.Version), nil
}

type hostAnswerError struct {
	refusal mcpRefusal
	code    string
}

func (e *hostAnswerError) Error() string { return e.refusal.Error() }

func (e *hostAnswerError) Unwrap() error { return &e.refusal }

func (d *Daemon) hostGet(ctx context.Context, qualifiedID, path, idParam string, query url.Values, out any) error {
	hostID, rawID, _ := hosts.Split(qualifiedID)
	host, paired := d.pairedHost(hostID)
	if !paired {
		return refuse("not_found", fmt.Sprintf("Host %s is no longer paired.", hostID))
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set(idParam, rawID)
	readCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	response, err := host.client.Do(readCtx, http.MethodGet, path, query, nil, nil)
	if err != nil {
		return &mcpRefusal{Code: "refused", Message: fmt.Sprintf("Host %s did not answer: %s", host.record.Name, err), Retryable: true}
	}
	body, err := hosts.ReadResponse(response)
	var status hosts.StatusError
	if errors.As(err, &status) {
		refusal, _ := mcpCodeFor(status.Status, status.Code)
		refusal.Message = fmt.Sprintf("Host %s: %s", host.record.Name, status.Message)
		return &hostAnswerError{refusal: refusal, code: status.Code}
	}
	if err != nil {
		return &mcpRefusal{Code: "refused", Message: fmt.Sprintf("Host %s did not answer: %s", host.record.Name, err), Retryable: true}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &mcpRefusal{Code: "refused", Message: fmt.Sprintf("Host %s answered with what this host cannot read: %s", host.record.Name, err), Retryable: true}
	}
	return nil
}
