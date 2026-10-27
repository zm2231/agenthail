package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/surface"
)

const proxiesUsage = "usage: agenthail proxies [--json] | add <name> <url> [--key-file <path> | --key-env <name> | --keychain-service <service> --keychain-account <account>] | key <name> <key reference> | rm <name>"

func (a *App) cmdProxies(args []string) error {
	positional := stripFlags(args)
	if len(positional) == 0 {
		return a.listProxies(args)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	config, proxies, err := readProxyConfigs(home)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(proxies, func(proxy proxyacct.Config) bool { return len(positional) > 1 && proxy.Name == positional[1] })
	ref, err := keyRefFromFlags(args)
	if err != nil {
		return err
	}
	switch {
	case positional[0] == "add" && len(positional) == 3:
		if index >= 0 {
			return fmt.Errorf("a proxy named %s is already configured; change its key with agenthail proxies key %s", positional[1], positional[1])
		}
		proxies = append(proxies, proxyacct.Config{Name: positional[1], Kind: proxyacct.KindCLIProxyAPI, URL: positional[2], Management: ref})
	case positional[0] == "key" && len(positional) == 2 && ref != nil:
		if index < 0 {
			return fmt.Errorf("no proxy is named %s", positional[1])
		}
		proxies[index].Management = ref
	case (positional[0] == "rm" || positional[0] == "remove") && len(positional) == 2:
		if index < 0 {
			return fmt.Errorf("no proxy is named %s", positional[1])
		}
		proxies = slices.Delete(proxies, index, index+1)
	default:
		return errors.New(proxiesUsage)
	}
	if err := writeProxyConfigs(home, config, proxies); err != nil {
		return err
	}
	fmt.Printf("saved proxy %s in %s\n", positional[1], surface.AgenthailConfigPath(home))
	return nil
}

type proxyView struct {
	usageSetView
	Roots []usageSetView `json:"roots"`
}

func (a *App) listProxies(args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	_, configured, err := readProxyConfigs(home)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var usage struct {
		Sets []usageSetView `json:"sets"`
	}
	if !a.daemonIsRunning() {
		views := []proxyView{}
		for _, config := range configured {
			views = append(views, proxyView{usageSetView: usageSetView{Provider: "proxy", Name: config.Name, Proxy: &usageProxyView{Name: config.Name, URL: config.URL}, Problem: "the Agenthail daemon is not running; its accounts are read by the daemon"}})
		}
		return writeProxies(os.Stdout, views, hasFlag(args, "--json"), time.Now())
	}
	if err := a.signinCall(ctx, http.MethodGet, "/api/v1/usage", nil, nil, &usage); err != nil {
		return err
	}
	return writeProxies(os.Stdout, proxyViews(usage.Sets), hasFlag(args, "--json"), time.Now())
}

func proxyViews(sets []usageSetView) []proxyView {
	views := []proxyView{}
	for _, set := range sets {
		if set.Provider == "proxy" {
			views = append(views, proxyView{usageSetView: set, Roots: []usageSetView{}})
		}
	}
	for _, set := range sets {
		if set.Provider != "claude" || set.Proxy == nil {
			continue
		}
		for i := range views {
			if views[i].Proxy != nil && views[i].Proxy.ID == set.Proxy.ID {
				views[i].Roots = append(views[i].Roots, set)
			}
		}
	}
	return views
}

func writeProxies(w io.Writer, views []proxyView, asJSON bool, now time.Time) error {
	if asJSON {
		return emit(map[string]any{"proxies": views})
	}
	if len(views) == 0 {
		fmt.Fprintln(w, "no proxies configured; add one with agenthail proxies add <name> <url> --key-file <path>")
		return nil
	}
	for _, view := range views {
		title := view.Name + " (proxy)"
		if view.Proxy != nil && view.Proxy.URL != "" {
			title += "  " + view.Proxy.URL
		}
		if view.Host != "" && !view.Local {
			title += "  read from " + view.Host
		}
		fmt.Fprintln(w, title)
		writeProxyAccounts(w, view.usageSetView, now, "  ")
		if len(view.Roots) > 0 {
			var roots []string
			for _, root := range view.Roots {
				roots = append(roots, root.Name+" on "+root.Host)
			}
			fmt.Fprintf(w, "  roots: %s\n", strings.Join(roots, ", "))
		}
	}
	return nil
}

func writeProxyAccounts(w io.Writer, set usageSetView, now time.Time, indent string) {
	for _, account := range set.Accounts {
		line := fmt.Sprintf("%s%-24s %s", indent, account.Label, proxyAccountState(account, now))
		for _, meter := range account.Meters {
			if !meter.Until.IsZero() && !now.Before(meter.Until) {
				continue
			}
			part := fmt.Sprintf("%s %.0f%%", usageKindLabel(meter), meter.Percent)
			if meter.Ceiling > 0 {
				part += fmt.Sprintf(" (cap %.0f%%)", meter.Ceiling)
			}
			line += "  " + part
		}
		if !account.AsOf.IsZero() {
			line += "  read " + usageDuration(now.Sub(account.AsOf)) + " ago"
		}
		fmt.Fprintln(w, line)
	}
	if set.Problem != "" {
		fmt.Fprintf(w, "%s%s\n", indent, set.Problem)
	}
	if set.Detail != "" {
		fmt.Fprintf(w, "%s%s\n", indent, set.Detail)
	}
}

func proxyAccountState(account usageAccountView, now time.Time) string {
	label := account.Provider
	switch account.Status {
	case proxyacct.StatusCooling:
		if !account.NextRetry.IsZero() {
			return label + ", cooling for " + usageDuration(account.NextRetry.Sub(now))
		}
		return label + ", cooling"
	case proxyacct.StatusDisabled, proxyacct.StatusUnavailable:
		return label + ", " + account.Status
	}
	return label
}
