package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerBacklogUsage = `usage: agenthail ledger backlog [--kind usage,idea,later,person] [--ready] [--limit N] [--json]
       agenthail ledger backlog --triage [--days N] [--limit N] [--json]`

type ledgerBacklogRow struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	ClauseID  string    `json:"clauseId,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Ready     bool      `json:"ready"`
	Why       string    `json:"why,omitempty"`
	ReadyAt   time.Time `json:"readyAt,omitzero"`
	State     string    `json:"state,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

type ledgerBacklogPage struct {
	Backlog   []ledgerBacklogRow `json:"backlog,omitempty"`
	Triage    []ledgerBacklogRow `json:"triage,omitempty"`
	IdleDays  int                `json:"idleDays,omitempty"`
	Total     int                `json:"total"`
	Truncated bool               `json:"truncated"`
}

func (a *App) ledgerBacklog(args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) != 1 {
		return fmt.Errorf("backlog takes no arguments\n%s", ledgerBacklogUsage)
	}
	triage := w.bools["--triage"]
	query := url.Values{"view": {"backlog"}}
	if triage {
		if w.value("--kind") != "" || w.bools["--ready"] {
			return fmt.Errorf("--triage lists unparked work; it takes --days, not --kind or --ready\n%s", ledgerBacklogUsage)
		}
		query.Set("view", "triage")
		if days := w.value("--days"); days != "" {
			query.Set("days", days)
		}
	} else if w.value("--days") != "" {
		return fmt.Errorf("--days goes with --triage\n%s", ledgerBacklogUsage)
	}
	for kind := range strings.SplitSeq(w.value("--kind"), ",") {
		if kind = strings.TrimSpace(kind); kind != "" {
			if _, ok := ledger.ParseParkKind(kind); !ok {
				return fmt.Errorf("--kind %q is not a park kind; use usage, idea, later or person", kind)
			}
			query.Add("kind", kind)
		}
	}
	if w.bools["--ready"] {
		query.Set("ready", "true")
	}
	if limit := w.value("--limit"); limit != "" {
		query.Set("limit", limit)
	}
	key := w.value("--project")
	if key == "" {
		if key, err = ledgerProjectKey(a); err != nil {
			return err
		}
	}
	query.Set("projectKey", key)
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	data, status, err := client.call(context.Background(), http.MethodGet, "/api/v1/ledger", query, nil, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return actionError(data)
	}
	var page ledgerBacklogPage
	if err := json.Unmarshal(data, &page); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(page)
	}
	printBacklog(page, triage)
	return nil
}

func printBacklog(page ledgerBacklogPage, triage bool) {
	rows := page.Backlog
	if triage {
		rows = page.Triage
	}
	switch {
	case len(rows) == 0 && triage:
		fmt.Printf("no open or blocked work has sat unowned for %d days\n", page.IdleDays)
		return
	case len(rows) == 0:
		fmt.Println("nothing is parked")
		return
	}
	for _, row := range rows {
		if triage {
			fmt.Printf("%s  %s  idle since %s  %s\n", row.ID, row.State, row.UpdatedAt.Local().Format("2006-01-02"), row.Text)
			continue
		}
		mark := "waiting"
		if row.Ready {
			mark = "ready"
		}
		fmt.Printf("%s  %s  %s: %s  %s\n", row.ID, mark, row.Kind, row.Why, row.Text)
	}
	if page.Truncated {
		fmt.Printf("showing %d of %d; --limit shows more\n", len(rows), page.Total)
	}
	if triage {
		fmt.Println("park them with: agenthail ledger park <id>... --later \"<why>\" (or --usage, --person, --idea)")
	}
}
