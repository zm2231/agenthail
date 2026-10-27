package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
)

const signinUsage = `usage: agenthail signin [list] [--json]
       agenthail signin add <browser> <profile> [--label <label>]
       agenthail signin use <consumer> <profile|auto|none>
       agenthail signin rm <profile>

Agenthail reads sign-in cookies only for Notion and for Claude sessions reached
over Remote Control. By default each reads the system default browser's last
used profile ('auto'). 'use' pins a consumer to a profile you added, and
'none' turns its sign-in off.
Consumers: notion, claude. Browsers: chrome, brave, arc, chromium, edge, firefox, safari.
A profile is named by its ID or label; 'list' shows the browser profiles on this computer.`

func describeSignin(c signin.Status) string {
	switch c.Mode {
	case signin.Off:
		return "off, reads no cookies"
	case signin.Chosen:
		return fmt.Sprintf("%s (%s %q)", c.ProfileName, c.BrowserLabel, c.Profile)
	}
	if c.BrowserLabel == "" {
		return "automatic (system default browser)"
	}
	if c.Browser == signin.Safari {
		return "Safari, automatic (system default browser)"
	}
	if c.ProfileName != "" && c.ProfileName != c.Profile {
		return fmt.Sprintf("%s %q (%s), automatic (system default browser)", c.BrowserLabel, c.Profile, c.ProfileName)
	}
	return fmt.Sprintf("%s %q, automatic (system default browser)", c.BrowserLabel, c.Profile)
}

// signinStatus asks the daemon, which is the process that reads cookies and
// holds the macOS permissions that matter. With the daemon down it resolves
// the same choices from the registry.
func (a *App) signinStatus(ctx context.Context) []signin.Status {
	if a.daemonIsRunning() {
		var listing signinListing
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := a.signinCall(callCtx, http.MethodGet, "/api/v1/signin", nil, nil, &listing)
		cancel()
		if err == nil {
			return listing.Consumers
		}
	}
	statuses := make([]signin.Status, 0, len(signin.Consumers))
	for _, consumer := range signin.Consumers {
		r, err := a.openRegistry()
		if err == nil && r == nil {
			err = errors.New("the registry is not open")
		}
		if err != nil {
			statuses = append(statuses, signin.NewStatus(consumer, signin.Selection{}, err))
			continue
		}
		selection, _, err := r.SigninSelection(consumer.ID)
		statuses = append(statuses, signin.NewStatus(consumer, selection, err))
	}
	return statuses
}

type signinListing struct {
	Profiles  []signin.Profile `json:"profiles"`
	Consumers []signin.Status  `json:"consumers"`
	Browsers  []struct {
		Browser   signin.Browser `json:"browser"`
		Label     string         `json:"label"`
		Available bool           `json:"available"`
		Detail    string         `json:"detail,omitempty"`
		Profiles  []struct {
			Profile   string `json:"profile"`
			Name      string `json:"name"`
			ProfileID string `json:"profileId,omitempty"`
		} `json:"profiles"`
	} `json:"browsers"`
}

func (a *App) cmdSignin(args []string) error {
	if hasFlag(args, "--help") {
		fmt.Println(signinUsage)
		return nil
	}
	positional := stripFlags(args)
	sub := "list"
	if len(positional) > 0 {
		sub = positional[0]
		positional = positional[1:]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch sub {
	case "list", "ls":
		if len(positional) != 0 {
			return fmt.Errorf("%s", signinUsage)
		}
		return a.signinList(ctx, hasFlag(args, "--json"))
	case "add":
		if len(positional) != 2 {
			return fmt.Errorf("%s", signinUsage)
		}
		payload := map[string]any{"browser": positional[0], "profile": positional[1], "label": flagVal(args, "--label")}
		var answer struct {
			Profile signin.Profile `json:"profile"`
		}
		if err := a.signinCall(ctx, http.MethodPost, "/api/v1/signin/profiles", nil, payload, &answer); err != nil {
			return err
		}
		fmt.Printf("Added %s (%s, %s %q).\n", answer.Profile.Label, answer.Profile.ID, answer.Profile.Browser.Label(), answer.Profile.Profile)
		fmt.Printf("Use it with: agenthail signin use <%s> %s\n", consumerList(), answer.Profile.ID)
		return nil
	case "use":
		if len(positional) != 2 {
			return fmt.Errorf("%s", signinUsage)
		}
		var answer signin.Status
		if err := a.signinCall(ctx, http.MethodPut, "/api/v1/signin/bindings", nil, map[string]any{"consumer": positional[0], "profileId": positional[1]}, &answer); err != nil {
			return err
		}
		if answer.Mode == signin.Off {
			fmt.Printf("%s sign-in is off and reads no cookies.\n", answer.Label)
		} else {
			fmt.Printf("%s reads cookies from %s.\n", answer.Label, describeSignin(answer))
		}
		if answer.Problem != "" {
			fmt.Printf("  problem: %s\n", answer.Problem)
		}
		if answer.Unavailable != "" {
			fmt.Printf("  unavailable: %s\n", answer.Unavailable)
		}
		return nil
	case "rm", "remove":
		if len(positional) != 1 {
			return fmt.Errorf("%s", signinUsage)
		}
		var answer struct {
			Profile signin.Profile `json:"profile"`
		}
		if err := a.signinCall(ctx, http.MethodDelete, "/api/v1/signin/profiles", url.Values{"id": []string{positional[0]}}, nil, &answer); err != nil {
			return err
		}
		fmt.Printf("Removed %s. Consumers that used it read the system default browser again.\n", answer.Profile.Label)
		return nil
	default:
		return fmt.Errorf("unknown signin command %q\n%s", sub, signinUsage)
	}
}

func (a *App) signinCall(ctx context.Context, method, path string, query url.Values, payload any, value any) error {
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	data, _, err := client.call(ctx, method, path, query, payload, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (a *App) signinList(ctx context.Context, jsonOut bool) error {
	var listing signinListing
	if err := a.signinCall(ctx, http.MethodGet, "/api/v1/signin", nil, nil, &listing); err != nil {
		return err
	}
	if jsonOut {
		return emit(listing)
	}
	labels := map[string]string{}
	for _, profile := range listing.Profiles {
		labels[profile.ID] = profile.Label
	}
	fmt.Println("Consumers:")
	for _, consumer := range listing.Consumers {
		fmt.Printf("  %-7s %s\n", consumer.ID, describeSignin(consumer))
		if consumer.Problem != "" {
			fmt.Printf("          problem: %s\n", consumer.Problem)
		}
		if consumer.Unavailable != "" {
			fmt.Printf("          unavailable: %s\n", consumer.Unavailable)
		}
	}
	fmt.Printf("  change: agenthail signin use <%s> <profile|auto|none>\n", consumerList())
	if roots, err := a.claudeConfigRoots(ctx); err == nil && len(roots) > 0 {
		fmt.Println("Claude config roots (Remote Control uses the profile signed into each root's login):")
		for _, root := range roots {
			if root.SignedInAs == nil {
				continue
			}
			profile := root.Signin
			if root.Proxy != "" {
				profile = "via the " + root.Proxy + " proxy"
			} else if root.SigninProblem != "" {
				profile = "no profile: " + root.SigninProblem
			}
			fmt.Printf("  %s  %s  %s\n", root.Name, root.SignedInAs.Email, profile)
		}
	}
	fmt.Println("Sign-in profiles:")
	if len(listing.Profiles) == 0 {
		fmt.Println("  none; to pin a consumer to another profile: agenthail signin add <browser> <profile>")
	}
	for _, profile := range listing.Profiles {
		fmt.Printf("  %s  %s  %s %q\n", profile.ID, profile.Label, profile.Browser.Label(), profile.Profile)
	}
	fmt.Println("Browser profiles on this computer:")
	if len(listing.Browsers) == 0 {
		fmt.Println("  no supported browser found")
	}
	for _, browser := range listing.Browsers {
		if !browser.Available {
			fmt.Printf("  %s (%s): unavailable, %s\n", browser.Browser, browser.Label, browser.Detail)
			continue
		}
		fmt.Printf("  %s (%s)\n", browser.Browser, browser.Label)
		for _, profile := range browser.Profiles {
			added := ""
			if profile.ProfileID != "" {
				added = "  added as " + labels[profile.ProfileID]
			}
			fmt.Printf("    %q  %s%s\n", profile.Profile, profile.Name, added)
		}
	}
	return nil
}

func consumerList() string {
	names := make([]string, len(signin.Consumers))
	for i, consumer := range signin.Consumers {
		names[i] = string(consumer.ID)
	}
	return strings.Join(names, "|")
}
