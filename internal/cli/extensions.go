package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const extensionsUsage = "usage: agenthail extensions [list] | approve <id> | reload | key <id> <mount> (--key-file <path> | --key-env <name> | --keychain-service <service> --keychain-account <account> | --key-command -- <argv...> | --clear), each with [--json]"

type extensionsListView struct {
	Host       string `json:"host"`
	HostError  string `json:"hostError"`
	Extensions []struct {
		ID           string   `json:"id"`
		Label        string   `json:"label"`
		State        string   `json:"state"`
		Error        string   `json:"error"`
		Page         string   `json:"page"`
		Capabilities []string `json:"capabilities"`
		Pending      []string `json:"pendingCapabilities"`
		Actions      []string `json:"actions"`
		Jobs         map[string]struct {
			LastRun   time.Time `json:"lastRun"`
			LastError string    `json:"lastError"`
		} `json:"jobs"`
		EventError string `json:"lastEventError"`
		Mounts     []struct {
			Name      string `json:"name"`
			Label     string `json:"label"`
			Port      int    `json:"port"`
			Entry     string `json:"entry"`
			Upstream  string `json:"upstream"`
			State     string `json:"state"`
			Error     string `json:"error"`
			Published bool   `json:"published"`
			Key       string `json:"key"`
			Signin    bool   `json:"signin"`
		} `json:"mounts"`
	} `json:"extensions"`
}

func (a *App) cmdExtensions(args []string) error {
	args, command, err := splitKeyCommand(args)
	if err != nil {
		return err
	}
	positional := stripFlags(args)
	sub := "list"
	if len(positional) > 0 {
		sub = positional[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	asJSON := hasFlag(args, "--json")
	var data []byte
	switch {
	case sub == "list" && len(positional) <= 1:
		data, _, err = client.call(ctx, http.MethodGet, "/api/v1/extensions", nil, nil, nil)
	case sub == "reload" && len(positional) == 1:
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions", nil, map[string]any{"action": "reload"}, nil)
	case sub == "approve" && len(positional) == 2:
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions", nil, map[string]any{"action": "approve", "id": positional[1]}, nil)
	case sub == "key" && len(positional) == 3:
		ref, refErr := keyRefFromFlags(args, command)
		if refErr != nil {
			return refErr
		}
		if (ref == nil) != hasFlag(args, "--clear") {
			return errors.New(extensionsUsage)
		}
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions", nil, map[string]any{"action": "key", "id": positional[1], "mount": positional[2], "key": ref}, nil)
	default:
		return errors.New(extensionsUsage)
	}
	if err != nil {
		return err
	}
	if asJSON {
		fmt.Println(string(data))
		return nil
	}
	var view extensionsListView
	if err := json.Unmarshal(data, &view); err != nil {
		return err
	}
	fmt.Print(describeExtensions(view))
	return nil
}

func describeExtensions(view extensionsListView) string {
	var out strings.Builder
	if view.Host != "running" {
		fmt.Fprintf(&out, "[extensions] host %s", view.Host)
		if view.HostError != "" {
			fmt.Fprintf(&out, ": %s", view.HostError)
		}
		out.WriteString("\n")
	}
	if len(view.Extensions) == 0 {
		out.WriteString("[extensions] none in ~/.agenthail/extensions\n")
	}
	for _, ext := range view.Extensions {
		fmt.Fprintf(&out, "%s  %s  %s\n", ext.ID, ext.State, ext.Label)
		if ext.Error != "" {
			fmt.Fprintf(&out, "  error: %s\n", ext.Error)
		}
		if ext.Page != "" {
			fmt.Fprintf(&out, "  page: %s\n", ext.Page)
		}
		if len(ext.Capabilities) > 0 {
			fmt.Fprintf(&out, "  holds: %s\n", strings.Join(ext.Capabilities, ", "))
		}
		if len(ext.Pending) > 0 {
			fmt.Fprintf(&out, "  waits for approval: %s (agenthail extensions approve %s)\n", strings.Join(ext.Pending, ", "), ext.ID)
		}
		if len(ext.Actions) > 0 {
			fmt.Fprintf(&out, "  actions: %s\n", strings.Join(ext.Actions, ", "))
		}
		names := make([]string, 0, len(ext.Jobs))
		for name := range ext.Jobs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			job := ext.Jobs[name]
			line := "never ran"
			if !job.LastRun.IsZero() {
				line = "last ran " + job.LastRun.Local().Format(time.DateTime)
			}
			if job.LastError != "" {
				line += ", failed: " + job.LastError
			}
			fmt.Fprintf(&out, "  job %s: %s\n", name, line)
		}
		if ext.EventError != "" {
			fmt.Fprintf(&out, "  last event handler error: %s\n", ext.EventError)
		}
		for _, mount := range ext.Mounts {
			fmt.Fprintf(&out, "  mount %s (%s): %s, %s", mount.Name, mount.Label, mount.State, mount.Upstream)
			if mount.Port > 0 {
				fmt.Fprintf(&out, " on port %d%s", mount.Port, mount.Entry)
			}
			if mount.Published {
				out.WriteString(", on the tailnet")
			}
			out.WriteString("\n")
			switch {
			case mount.Error != "":
				fmt.Fprintf(&out, "    error: %s\n", mount.Error)
			case mount.State == "pending":
				fmt.Fprintf(&out, "    waits for approval (agenthail extensions approve %s)\n", ext.ID)
			case mount.Key != "":
				fmt.Fprintf(&out, "    key: %s\n", mount.Key)
			case mount.Signin:
				fmt.Fprintf(&out, "    signs in once you name its key (agenthail extensions key %s %s --key-file <path>)\n", ext.ID, mount.Name)
			}
		}
	}
	return out.String()
}
