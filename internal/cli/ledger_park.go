package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func (a *App) ledgerPark(w ledgerWriteArgs, req ledger.WorkRequest) error {
	ids := w.positional[2:]
	if len(ids) == 0 {
		return fmt.Errorf("%s needs at least one item\n%s", req.Action, ledgerWorkUsage)
	}
	if req.Action == ledger.WorkPark {
		park, err := parkFromFlags(w, time.Now())
		if err != nil {
			return err
		}
		req.Park, req.ForSession = &park, w.value("--for-session")
	}
	ctx := context.Background()
	var err error
	if req.ProjectKey, req.SessionID, err = a.ledgerWriteContext(ctx, w); err != nil {
		return err
	}
	results := make([]ledger.WorkResult, 0, len(ids))
	var failed []error
	for _, id := range ids {
		one := req
		one.ItemID = id
		var result ledger.WorkResult
		if err := a.ledgerPost(ctx, "/api/v1/ledger/work", one, &result); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", id, err))
			if !w.bools["--json"] {
				fmt.Printf("%s not %sed: %s\n", id, req.Action, err)
			}
			continue
		}
		results = append(results, result)
		if !w.bools["--json"] {
			fmt.Println(result.Readback)
		}
	}
	if w.bools["--json"] {
		errs := make([]string, len(failed))
		for i, err := range failed {
			errs[i] = err.Error()
		}
		if err := emit(struct {
			Results []ledger.WorkResult `json:"results"`
			Errors  []string            `json:"errors,omitempty"`
		}{results, errs}); err != nil {
			return err
		}
	}
	return errors.Join(failed...)
}

func parkFromFlags(w ledgerWriteArgs, now time.Time) (ledger.Park, error) {
	park := ledger.Park{Why: w.value("--why")}
	kinds := 0
	if target := w.value("--usage"); target != "" {
		kinds++
		provider, root, _ := strings.Cut(target, ":")
		park.Kind, park.Usage = ledger.ParkUsage, &ledger.ParkUsageTarget{Provider: provider, Root: root}
	}
	if why := w.value("--later"); why != "" {
		kinds++
		park.Kind = ledger.ParkLater
		if park.Why != "" {
			why += "; " + park.Why
		}
		park.Why = why
	}
	if question := w.value("--person"); question != "" {
		kinds++
		park.Kind, park.Person = ledger.ParkPerson, &ledger.ParkQuestion{Question: question, AskID: w.value("--ask")}
	}
	if w.bools["--idea"] {
		kinds++
		park.Kind, park.Idea = ledger.ParkIdea, &ledger.ParkIdeaShape{UtteranceID: w.value("--utterance"), Goal: w.value("--goal"), Plan: w.value("--plan"), FirstTest: w.value("--first-test")}
	}
	if kinds != 1 {
		return ledger.Park{}, fmt.Errorf("park takes exactly one of --usage, --later, --person or --idea\n%s", ledgerWorkUsage)
	}
	if until := w.value("--until"); until != "" {
		at, err := parseRevisit(until, now)
		if err != nil {
			return ledger.Park{}, err
		}
		park.Revisit = at
	}
	return park, nil
}

func parseRevisit(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if at, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return at.UTC(), nil
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at.UTC(), nil
	}
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if count, ok := strings.CutSuffix(value, suffix); ok {
			if n, err := strconv.Atoi(count); err == nil && n > 0 {
				return now.Add(time.Duration(n) * unit).UTC(), nil
			}
		}
	}
	if span, err := time.ParseDuration(value); err == nil && span > 0 {
		return now.Add(span).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("--until takes a date (2026-12-01), an RFC 3339 time, or a span such as 36h, 7d or 2w, not %q", value)
}

func parkLine(park ledger.Park) string {
	line := fmt.Sprintf("parked (%s): %s", park.Kind, park.Summary())
	if park.Session != "" {
		line += " by " + park.Session
	}
	return line
}
