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
	var sessionRead *surface.SessionReadResult
	var sessionReadErr error
	if historyBefore, provider := decodeProviderHistoryCursor(timelineBefore); provider {
		historyCtx, cancel := context.WithTimeout(r.Context(), operationTimeout)
		sessionRead, sessionReadErr = d.readProviderHistoryPage(historyCtx, session, adapter, historyBefore, limit)
		cancel()
	} else if timelineBefore > 0 {
		sessionRead, sessionReadErr = d.readJournalPage(session.ID, uint64(timelineBefore), limit)
	} else {
		seedCtx, cancel := context.WithTimeout(r.Context(), min(operationTimeout, sessionPageCatchUpWait))
		seedErr := d.sources.seed(seedCtx, session, adapter)
		cancel()
		sessionRead, sessionReadErr = d.readJournalPage(session.ID, 0, limit)
		if sessionReadErr == nil && seedErr != nil && sessionRead.JournalSeq == 0 {
			sessionReadErr = seedErr
		}
	}
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
	exchanges, transcript := truncateSessionExchanges(sessionRead.Exchanges)
	response := map[string]any{"session": session, "alias": alias, "limitResume": d.limitResumeRow(session.ID), "exchanges": exchanges, "capabilities": effective.Capabilities, "readOnly": effective.ReadOnly, "readOnlyReason": effective.ReadOnlyReason, "readSource": sessionRead.Source, "transcriptTruncated": transcript.Truncated || sessionRead.Truncated, "transcriptOriginalBytes": transcript.OriginalBytes, "transcriptReturnedBytes": transcript.ReturnedBytes, "transcriptOriginalExchanges": transcript.OriginalExchanges, "transcriptReturnedExchanges": len(exchanges)}
	response["journalSeq"] = sessionRead.JournalSeq
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
