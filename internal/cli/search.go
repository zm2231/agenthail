package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdSearch(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 || positional[0] != "codex" {
		return fmt.Errorf("usage: agenthail search codex <query> [--json]")
	}
	adapter := a.adapters().ForSurface(surface.KindCodex)
	if adapter == nil {
		return fmt.Errorf("Codex is not configured")
	}
	searcher, ok := adapter.(surface.SessionSearcher)
	if !ok {
		return fmt.Errorf("Codex history search is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	query := strings.Join(positional[1:], " ")
	if len([]rune(strings.TrimSpace(query))) < 3 {
		return fmt.Errorf("Codex history search requires at least 3 characters")
	}
	results := make([]surface.SessionSearchResult, 0)
	if a.registry() != nil {
		stored, err := a.registry().SearchSessions(surface.KindCodex, query, 20)
		if err != nil {
			return fmt.Errorf("search saved Codex conversations: %w", err)
		}
		for _, session := range stored {
			results = append(results, surface.SessionSearchResult{Session: session, Snippet: "Saved by Agenthail"})
		}
	}
	remote, err := searcher.SearchSessions(ctx, query, 20)
	remoteError := ""
	if err != nil {
		if errors.Is(err, surface.ErrUnsupported) {
			remoteError = "Codex history search is unavailable in this Codex version"
		} else {
			remoteError = err.Error()
		}
	} else {
		results = mergeSearchResults(results, remote)
	}
	for _, result := range results {
		if a.registry() != nil {
			if err := a.registry().RegisterSession(result.Session); err != nil {
				return fmt.Errorf("register Codex search result: %w", err)
			}
		}
	}
	if hasFlag(args, "--json") {
		return emit(map[string]any{"results": results, "remoteError": remoteError})
	}
	if remoteError != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", remoteError)
	}
	if len(results) == 0 {
		fmt.Println("no Codex conversations matched")
		return nil
	}
	for _, result := range results {
		fmt.Printf("%s  %s  %s\n", result.Session.ID, result.Session.Name, result.Snippet)
	}
	return nil
}

func mergeSearchResults(left, right []surface.SessionSearchResult) []surface.SessionSearchResult {
	byID := make(map[string]surface.SessionSearchResult, len(left)+len(right))
	for _, result := range left {
		byID[result.Session.ID] = result
	}
	for _, result := range right {
		byID[result.Session.ID] = result
	}
	output := make([]surface.SessionSearchResult, 0, len(byID))
	for _, result := range byID {
		output = append(output, result)
	}
	sort.Slice(output, func(i, j int) bool { return output[i].Session.LastActive.After(output[j].Session.LastActive) })
	return output
}

func queuedTime(value string) time.Time {
	t, err := time.ParseInLocation(time.DateTime, value, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

func queueStatusLabel(row registry.QueueRow, remote bool) string {
	if row.Evidence == "" || row.Status == "canceled" || row.Evidence == registry.QueueStatusEvidence(row.Status) {
		return row.Status
	}
	evidence := string(row.Evidence)
	evidence = strings.ReplaceAll(evidence, "_", " ")
	if remote && row.Status == "delivered" {
		evidence = "remote " + evidence
	}
	return row.Status + " (" + evidence + ")"
}

func queueHoldLabel(hold registry.QueueHold) string {
	switch hold {
	case registry.QueueHeldWaiting:
		return "held: target is waiting on you"
	case registry.QueueHeldBusy:
		return "held: target is busy"
	case registry.QueueHeldOffline:
		return "held: target is offline"
	case registry.QueueHeldUnknown:
		return "held: target state is unknown"
	case registry.QueueHeldRefused:
		return "held: target refused to start; see the last error"
	case registry.QueueHeldRetrying:
		return "held: waiting to retry"
	case registry.QueueHeldBehind:
		return "held: behind an earlier message"
	case registry.QueueHeldUnloaded:
		return "held: conversation is not loaded"
	case registry.QueueHeldHostUnreachable:
		return "held: host is unreachable"
	}
	return "delivers on the next daemon scan"
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
