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
		return d.mcpReadRemoteBody(ctx, qualifiedID, input.BodyRef, input.Offset)
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

func (d *Daemon) mcpReadRemoteBody(ctx context.Context, qualifiedID, ref string, offset int) (any, error) {
	if offset < 0 {
		return nil, refuse("invalid", "offset must not be negative.")
	}
	query := url.Values{"ref": {ref}, "start": {strconv.Itoa(offset)}, "end": {strconv.Itoa(offset + mcpBodyChunk)}}
	var answer struct {
		End   int    `json:"end"`
		Total int    `json:"total"`
		Body  string `json:"body"`
	}
	var failure *hostAnswerError
	if err := d.hostGet(ctx, qualifiedID, "/api/v1/session-stream-body", "id", query, &answer); errors.As(err, &failure) && failure.code == "body_unavailable" {
		return nil, refuse("not_found", "That body is no longer retained.")
	} else if errors.As(err, &failure) && failure.code == "invalid_range" {
		return nil, refuse("invalid", "The body range is invalid.")
	} else if err != nil {
		return nil, err
	}
	body := []byte(answer.Body)
	if offset+len(body) < answer.Total {
		body = wholeRunes(body)
	}
	end := offset + len(body)
	result := map[string]any{"sessionId": qualifiedID, "bodyRef": ref, "offset": offset, "end": end, "total": answer.Total, "text": string(body), "truncated": end < answer.Total}
	if end < answer.Total {
		result["nextOffset"] = end
	}
	return result, nil
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
	if err != nil {
		return &mcpRefusal{Code: "refused", Message: fmt.Sprintf("Host %s did not answer: %s", host.record.Name, err), Retryable: true}
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		refusal, _ := mcpCodeFor(response.StatusCode, failure.Error.Code)
		refusal.Message = fmt.Sprintf("Host %s: %s", host.record.Name, failure.Error.Message)
		return &hostAnswerError{refusal: refusal, code: failure.Error.Code}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &mcpRefusal{Code: "refused", Message: fmt.Sprintf("Host %s answered with what this host cannot read: %s", host.record.Name, err), Retryable: true}
	}
	return nil
}
