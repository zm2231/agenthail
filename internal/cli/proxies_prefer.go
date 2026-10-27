package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const proxiesPreferUsage = "usage: agenthail proxies prefer <proxy> <account> on|off | prefer <proxy> <account> --only [--now] [--yes] [--json]"

type preferPlanView struct {
	Proxy   *usageProxyView `json:"proxy"`
	Host    string          `json:"host"`
	Account struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"account"`
	Set     string `json:"set"`
	Now     bool   `json:"now"`
	Changes []struct {
		ID     string  `json:"id"`
		Label  string  `json:"label"`
		Before float64 `json:"before"`
		After  float64 `json:"after"`
	} `json:"changes"`
	Release []struct {
		Label string `json:"label"`
	} `json:"release"`
	Hosts []struct {
		Host     string   `json:"host"`
		Roots    []string `json:"roots"`
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	} `json:"hosts"`
	Nothing  string   `json:"nothing,omitempty"`
	Confirm  string   `json:"confirm,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	PlanID   string   `json:"planId"`
}

func (a *App) cmdProxiesPrefer(args []string, positional []string) error {
	set := ""
	switch {
	case len(positional) == 4 && (positional[3] == "on" || positional[3] == "off") && !hasFlag(args, "--only"):
		set = positional[3]
	case len(positional) == 3 && hasFlag(args, "--only"):
		set = "only"
	default:
		return errors.New(proxiesPreferUsage)
	}
	if hasFlag(args, "--json") && hasFlag(args, "--yes") {
		return errors.New("--json prints the plan without writing; drop --json to apply it with --yes")
	}
	if hasFlag(args, "--now") && set != "only" {
		return errors.New("--now applies only to --only: it releases the sessions on the accounts it turns off")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var usage struct {
		Sets []usageSetView `json:"sets"`
	}
	if err := a.signinCall(ctx, http.MethodGet, "/api/v1/usage", nil, nil, &usage); err != nil {
		return err
	}
	proxyID, accountID, err := resolvePreferTarget(proxyViews(usage.Sets), positional[1], positional[2])
	if err != nil {
		return err
	}
	now := hasFlag(args, "--now")
	query := url.Values{"proxy": {proxyID}, "account": {accountID}, "set": {set}, "now": {fmt.Sprint(now)}}
	var plan preferPlanView
	if err := a.signinCall(ctx, http.MethodGet, "/api/v1/proxies/prefer", query, nil, &plan); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(plan)
	}
	writePreferPlan(os.Stdout, plan)
	if plan.Nothing != "" {
		return nil
	}
	if !hasFlag(args, "--yes") && !confirmLine(os.Stdin, os.Stdout, plan.Confirm+"? [y/N] ") {
		fmt.Println("nothing changed")
		return nil
	}
	var result struct {
		Message string `json:"message"`
	}
	request := map[string]any{"proxy": proxyID, "account": accountID, "set": set, "now": now, "planId": plan.PlanID}
	if err := a.signinCall(ctx, http.MethodPost, "/api/v1/proxies/prefer", nil, request, &result); err != nil {
		return err
	}
	fmt.Println(result.Message)
	return nil
}

func resolvePreferTarget(views []proxyView, proxyArg, accountArg string) (string, string, error) {
	var matches []proxyView
	for _, view := range views {
		if view.Proxy != nil && (view.Proxy.ID == proxyArg || view.Name == proxyArg) {
			matches = append(matches, view)
		}
	}
	if len(matches) == 0 {
		return "", "", fmt.Errorf("no proxy is named %s; agenthail proxies lists them", proxyArg)
	}
	if len(matches) > 1 {
		ids := []string{}
		for _, match := range matches {
			ids = append(ids, match.Proxy.ID)
		}
		return "", "", fmt.Errorf("more than one proxy is named %s; name it by id: %s", proxyArg, strings.Join(ids, ", "))
	}
	proxy := matches[0]
	if len(proxy.Accounts) == 0 {
		if proxy.Problem != "" {
			return "", "", errors.New(proxy.Problem)
		}
		return "", "", fmt.Errorf("the %s proxy's accounts are not readable from any host", proxy.Name)
	}
	var found []usageAccountView
	for _, account := range proxy.Accounts {
		local, _, _ := strings.Cut(account.Label, "@")
		if account.ID == accountArg || account.Label == accountArg || local == accountArg {
			found = append(found, account)
		}
	}
	switch len(found) {
	case 0:
		return "", "", fmt.Errorf("the %s proxy has no account %s", proxy.Name, accountArg)
	case 1:
		return proxy.Proxy.ID, found[0].ID, nil
	}
	ids := []string{}
	for _, account := range found {
		ids = append(ids, account.ID+" ("+account.Provider+")")
	}
	return "", "", fmt.Errorf("%s names more than one account; name it by id: %s", accountArg, strings.Join(ids, ", "))
}

func writePreferPlan(w io.Writer, plan preferPlanView) {
	if plan.Proxy != nil {
		fmt.Fprintf(w, "%s proxy, read from %s\n", plan.Proxy.Name, plan.Host)
	}
	if plan.Nothing != "" {
		fmt.Fprintf(w, "  %s Nothing to change.\n", plan.Nothing)
		return
	}
	for _, change := range plan.Changes {
		fmt.Fprintf(w, "  %-24s %s -> %s\n", change.Label, preferenceWord(change.Before), preferenceWord(change.After))
	}
	if plan.Set != "only" {
		fmt.Fprintln(w, "  other accounts are not changed")
	}
	if len(plan.Release) > 0 {
		labels := []string{}
		for _, account := range plan.Release {
			labels = append(labels, account.Label)
		}
		fmt.Fprintf(w, "  releases the sessions on %s, so they move on their next request\n", strings.Join(labels, ", "))
	}
	if len(plan.Hosts) > 0 {
		fmt.Fprintln(w, "affects")
	}
	for _, host := range plan.Hosts {
		sessions := []string{}
		for _, session := range host.Sessions {
			sessions = append(sessions, session.Name)
		}
		line := fmt.Sprintf("  %s: %s", host.Host, strings.Join(host.Roots, ", "))
		if len(sessions) > 0 {
			line += "; running: " + strings.Join(sessions, ", ")
		}
		fmt.Fprintln(w, line)
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintln(w, warning)
	}
}

func preferenceWord(value float64) string {
	switch {
	case value == 0:
		return "not preferred"
	case value >= 10:
		return "preferred"
	}
	return fmt.Sprintf("preference %g", value)
}

func confirmLine(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
