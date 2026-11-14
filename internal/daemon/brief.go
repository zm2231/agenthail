package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	briefFallback   = 24 * time.Hour
	briefQuoteRunes = 200
)

type briefSince struct {
	At       time.Time `json:"at"`
	Fallback bool      `json:"fallback"`
}

func (d *Daemon) briefRequest(spec registry.CoordinatorSpec, sourceSessionID string) (string, *briefSince, error) {
	lead := d.coordinator().LeadSession(spec)
	if lead != "" && sourceSessionID == lead {
		return "", nil, invalidRequest("you are this project's lead; a brief is asked of the lead, not by it")
	}
	last, err := d.Registry.PersonLastMessage(lead, spec.ProjectID)
	if err != nil {
		return "", nil, err
	}
	since := &briefSince{At: time.Now().Add(-briefFallback).UTC(), Fallback: true}
	anchor := "There is no message from the person to you or this project's sessions in this host's records, so cover the last 24 hours."
	if last != nil {
		since = &briefSince{At: last.At.UTC()}
		anchor = fmt.Sprintf("That is when the person last wrote to you or one of this project's sessions; that message began: %q.", briefQuote(last.Message))
	}
	stamp := since.At.Format(time.RFC3339)
	return fmt.Sprintf(`Catch me up: brief the person on this project, covering everything since %s. %s
Write it from primary records, not memory: session transcripts and turns, delivery history, pull requests and CI, asks and the ledger since %s. Say what finished, what is in flight and who owns it, what is blocked and on whom, and what needs the person. Mark anything you inferred rather than read.`, stamp, anchor, stamp), since, nil
}

func briefQuote(message string) string {
	line := strings.Join(strings.Fields(message), " ")
	if utf8.RuneCountInString(line) <= briefQuoteRunes {
		return line
	}
	return string([]rune(line)[:briefQuoteRunes]) + "…"
}

func (d *Daemon) apiProjectBriefHandler(w http.ResponseWriter, r *http.Request) {
	p := principalFromRequest(r)
	if p.Kind == principalSession {
		writeAPIError(w, http.StatusForbidden, "person_only", "a brief is the person's request; a session writes to its lead with agenthail_send")
		return
	}
	key := r.Header.Get(actionIdempotencyHeader)
	if len(strings.TrimSpace(key)) > registry.ActionReceiptKeyLimit {
		writeAPIError(w, http.StatusBadRequest, "invalid_idempotency_key", "The Idempotency-Key is too long.")
		return
	}
	if extra, err := io.ReadAll(io.LimitReader(r.Body, 1)); err != nil || len(extra) > 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "a brief takes no request body")
		return
	}
	request := map[string]string{"action": "coordinator-brief", "project": r.PathValue("id"), "author": string(surface.AuthorPerson)}
	if host, project, qualified := hosts.Split(request["project"]); qualified {
		request["host"], request["project"] = host, project
	}
	body, _ := json.Marshal(request)
	d.performAction(r.Context(), p, actionFormatAPIv1, body, key).write(w)
}
