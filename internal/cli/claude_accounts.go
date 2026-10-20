package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdAccounts(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail accounts [--json]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	accounts, err := surface.ClaudeAccounts(home)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"claude": accounts})
	}
	now := time.Now()
	for _, account := range accounts {
		name := account.Name
		if account.Primary {
			name += " (primary)"
		}
		fmt.Printf("%s  %s\n  %s\n", name, account.Root, describeClaudeAccount(account, now))
	}
	return nil
}

func describeClaudeAccount(account surface.ClaudeAccount, now time.Time) string {
	if account.Error != "" {
		return "unreadable: " + account.Error
	}
	if !account.SignedIn {
		return "signed out"
	}
	parts := []string{account.Email}
	if detail := strings.Trim(strings.Join(nonEmpty(account.Organization, account.Plan), ", "), ", "); detail != "" {
		parts[0] += " (" + detail + ")"
	}
	if account.Usage != nil {
		if until, out := account.Usage.Exhausted(now); out {
			parts = append(parts, "out of usage until "+until.Local().Format("Jan 2 15:04"))
		}
		var limits []string
		for _, limit := range account.Usage.Limits {
			label := limit.Kind
			if limit.Scope != "" {
				label += " " + limit.Scope
			}
			limits = append(limits, fmt.Sprintf("%s %.0f%%", label, limit.Percent))
		}
		if len(limits) > 0 {
			parts = append(parts, "usage "+strings.Join(limits, ", ")+" as of "+account.Usage.FetchedAt.Local().Format("Jan 2 15:04"))
		}
	}
	if len(account.SameAccountAs) > 0 {
		parts = append(parts, "same account as "+strings.Join(account.SameAccountAs, ", "))
	}
	return strings.Join(parts, "; ")
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
