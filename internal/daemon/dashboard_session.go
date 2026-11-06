package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) readSessionPage(ctx context.Context, session *surface.Session, adapter surface.Surface, before int64, limit int, operationTimeout time.Duration, wait bool) (*surface.SessionReadResult, bool, error) {
	if historyBefore, provider := decodeProviderHistoryCursor(before); provider {
		historyCtx, cancel := context.WithTimeout(ctx, operationTimeout)
		defer cancel()
		read, err := d.readProviderHistoryPage(historyCtx, session, adapter, historyBefore, limit)
		return read, false, err
	}
	if before > 0 {
		read, err := d.readJournalPage(session.ID, uint64(before), limit)
		return read, false, err
	}
	if !wait && d.sources.journalServable(session, adapter) {
		caughtUp := d.sources.prime(ctx, session, adapter)
		read, err := d.readJournalPage(session.ID, 0, limit)
		return read, !caughtUp, err
	}
	seedCtx, cancel := context.WithTimeout(ctx, min(operationTimeout, sessionPageCatchUpWait))
	seedErr := d.sources.seed(seedCtx, session, adapter)
	cancel()
	read, err := d.readJournalPage(session.ID, 0, limit)
	if err == nil && seedErr != nil && read.JournalSeq == 0 {
		err = seedErr
	}
	return read, false, err
}

func (d *Daemon) dashboardSessionHandler(w http.ResponseWriter, r *http.Request) {
	d.dashboardSessionHandlerWithTimeout(w, r, surfaceOperationTimeout)
}

func (d *Daemon) dashboardSessionHandlerWithTimeout(w http.ResponseWriter, r *http.Request, operationTimeout time.Duration) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	sessionID := r.URL.Query().Get("id")
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "session id is required")
		return
	}
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		if aliased, aliasErr := d.Registry.LookupAlias(strings.TrimPrefix(sessionID, "@")); aliasErr == nil {
			session, err = d.Registry.Session(aliased)
		}
	}
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	adapter, adapterErr := d.adapters().For(session)
	if adapterErr != nil {
		writeAPIError(w, http.StatusConflict, "conflict", "surface is not configured")
		return
	}
	limit := 10
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || parsed < 1 || parsed > surface.MaxExchangePage {
			writeAPIError(w, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("Use a limit between 1 and %d exchanges.", surface.MaxExchangePage))
			return
		}
		limit = parsed
	}
	alias, _ := d.Registry.ReverseAlias(session.ID)
	effective := surface.EffectiveCapabilities(adapter, session)
	var timelineBefore int64
	if raw := r.URL.Query().Get("timelineBefore"); raw != "" {
		var parseErr error
		timelineBefore, parseErr = strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || timelineBefore < 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid activity cursor")
			return
		}
	}
	sessionRead, catchingUp, sessionReadErr := d.readSessionPage(r.Context(), session, adapter, timelineBefore, limit, operationTimeout, r.URL.Query().Get("wait") == "1")
	var historyGap *registry.SessionJournalHistoryGapError
	if errors.As(sessionReadErr, &historyGap) {
		writeDashboardJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":    "history_gap",
				"message": "The requested older session history is no longer retained.",
			},
			"earliestPosition": historyGap.Earliest,
			"latestPosition":   historyGap.Latest,
		})
		return
	}
	response := map[string]any{"session": session, "alias": alias, "limitResume": d.limitResumeRow(session.ID), "exchangeCount": len(sessionRead.Exchanges), "capabilities": effective.Capabilities, "readOnly": effective.ReadOnly, "readOnlyReason": effective.ReadOnlyReason, "readSource": sessionRead.Source, "transcriptTruncated": sessionRead.Truncated}
	if timeline := r.URL.Query().Get("timeline") == "1"; !timeline || sessionRead.UnavailableReason != "" {
		exchanges, transcript := truncateSessionExchanges(sessionRead.Exchanges)
		response["exchanges"] = exchanges
		response["transcriptTruncated"] = transcript.Truncated || sessionRead.Truncated
		response["transcriptOriginalBytes"] = transcript.OriginalBytes
		response["transcriptReturnedBytes"] = transcript.ReturnedBytes
		response["transcriptOriginalExchanges"] = transcript.OriginalExchanges
		response["transcriptReturnedExchanges"] = len(exchanges)
	}
	response["journalSeq"] = sessionRead.JournalSeq
	response["catchingUp"] = catchingUp
	if sessionReadErr != nil {
		response["readError"] = "Session journal could not be read."
	} else if sessionRead.UnavailableReason != "" {
		response["readError"] = sessionRead.UnavailableReason
	}
	if r.URL.Query().Get("timeline") == "1" {
		response["timeline"] = &surface.SessionTimeline{Items: sessionRead.Items, NextBefore: sessionRead.NextBefore, Source: sessionRead.Source, Truncated: sessionRead.Truncated, UnavailableReason: sessionRead.UnavailableReason}
	}
	writeDashboardJSON(w, http.StatusOK, response)
}
