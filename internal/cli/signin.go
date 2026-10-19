package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
)

const signinUsage = `usage: agenthail signin [list] [--json]
       agenthail signin add <browser> <profile> [--label <label>]
       agenthail signin use <consumer> <profile|none>
       agenthail signin rm <profile>

Agenthail reads sign-in cookies from the browser profile you choose, only for
Notion and for Claude sessions reached over Remote Control. A consumer with no
profile is not set up and never reads cookies.
Consumers: notion, claude. Browsers: chrome, brave, arc, chromium, edge, firefox, safari.
A profile is named by its ID or label; 'list' shows the browser profiles on this computer.`

type signinStatusEntry struct {
	Consumer signin.Consumer `json:"consumer"`
	Label    string          `json:"label"`
	Profile  *signin.Profile `json:"profile"`
	Error    string          `json:"error,omitempty"`
}

// signinStatus reads the bindings straight from the registry so doctor can
// report them while the daemon is down.
func (a *App) signinStatus() []signinStatusEntry {
	entries := make([]signinStatusEntry, 0, len(signin.Consumers))
	var bindings map[signin.Consumer]string
	var readErr error
	if a.Registry != nil {
		bindings, readErr = a.Registry.SigninBindings()
	}
	for _, consumer := range signin.Consumers {
		entry := signinStatusEntry{Consumer: consumer.ID, Label: consumer.Label}
		switch {
		case readErr != nil:
			entry.Error = readErr.Error()
		case bindings[consumer.ID] != "":
			profile, err := a.Registry.SigninProfile(bindings[consumer.ID])
			if err != nil {
				entry.Error = err.Error()
				break
			}
			entry.Profile = &profile
			if _, err := a.Registry.ResolveSignin(signin.Target{Consumer: consumer.ID}); err != nil {
				entry.Error = err.Error()
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

type signinListing struct {
	Profiles  []signin.Profile `json:"profiles"`
	Consumers []struct {
		ID        signin.Consumer `json:"id"`
		Label     string          `json:"label"`
		Purpose   string          `json:"purpose"`
		ProfileID *string         `json:"profileId"`
	} `json:"consumers"`
	Browsers []struct {
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
		var profileID any
		if positional[1] != "none" {
			profileID = positional[1]
		}
		var answer struct {
			Consumer signin.Consumer `json:"consumer"`
			Profile  *signin.Profile `json:"profile"`
		}
		if err := a.signinCall(ctx, http.MethodPut, "/api/v1/signin/bindings", nil, map[string]any{"consumer": positional[0], "profileId": profileID}, &answer); err != nil {
			return err
		}
		if answer.Profile == nil {
			fmt.Printf("%s is not set up and reads no cookies.\n", answer.Consumer.Label())
		} else {
			fmt.Printf("%s reads cookies from %s (%s %q).\n", answer.Consumer.Label(), answer.Profile.Label, answer.Profile.Browser.Label(), answer.Profile.Profile)
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
		fmt.Printf("Removed %s. Consumers that used it are not set up until you choose another profile.\n", answer.Profile.Label)
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
		return json.NewEncoder(os.Stdout).Encode(listing)
	}
	labels := map[string]string{}
	for _, profile := range listing.Profiles {
		labels[profile.ID] = profile.Label
	}
	fmt.Println("Consumers:")
	for _, consumer := range listing.Consumers {
		if consumer.ProfileID == nil {
			fmt.Printf("  %-7s not set up (%s)\n", consumer.ID, consumer.Label)
			continue
		}
		fmt.Printf("  %-7s %s (%s)\n", consumer.ID, labels[*consumer.ProfileID], *consumer.ProfileID)
	}
	fmt.Println("Sign-in profiles:")
	if len(listing.Profiles) == 0 {
		fmt.Println("  none; add one with: agenthail signin add <browser> <profile>")
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
