package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

const extensionsUsage = "usage: agenthail extensions [list] | build \"<what it should do>\" [--agent claude|codex] | call <id> <action> [--input <json>] | run <id> <job> | approve <id> | reload | history | undo <rev> | key <id> <mount> (--key-file <path> | --key-env <name> | --keychain-service <service> --keychain-account <account> | --key-command -- <argv...> | --clear), each with [--json]"

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
	timeout := 45 * time.Second
	if sub == "run" {
		timeout = 11 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	asJSON := hasFlag(args, "--json")
	caller := ""
	if agentEnvironment() && (sub == "approve" || sub == "key" || sub == "call" || sub == "run") {
		if caller, err = a.sourceSessionID(ctx, ""); err != nil {
			return err
		}
	}
	var data []byte
	switch {
	case sub == "build" && len(positional) >= 2:
		if agentEnvironment() {
			return errors.New("starting a builder is the person's to do; to work on an extension yourself, edit ~/.agenthail/extensions and read its AGENTS.md")
		}
		return a.buildExtension(ctx, client, strings.Join(positional[1:], " "), flagVal(args, "--agent"), asJSON)
	case sub == "call" && len(positional) == 3:
		input := json.RawMessage(flagVal(args, "--input"))
		if len(input) > 0 && !json.Valid(input) {
			return errors.New("--input must be JSON")
		}
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions/"+url.PathEscape(positional[1])+"/call", nil, map[string]any{"action": positional[2], "input": input, "sessionId": caller}, nil)
		if err == nil && !asJSON {
			return printExtensionCall(data)
		}
	case sub == "run" && len(positional) == 3:
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions/"+url.PathEscape(positional[1])+"/call", nil, map[string]any{"job": positional[2], "sessionId": caller}, nil)
		if err == nil && !asJSON {
			return printExtensionCall(data)
		}
	case sub == "list" && len(positional) <= 1:
		data, _, err = client.call(ctx, http.MethodGet, "/api/v1/extensions", nil, nil, nil)
	case sub == "reload" && len(positional) == 1:
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions", nil, map[string]any{"action": "reload"}, nil)
	case sub == "approve" && len(positional) == 2:
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions", nil, map[string]any{"action": "approve", "id": positional[1], "sessionId": caller}, nil)
	case sub == "history" && len(positional) == 1:
		data, _, err = client.call(ctx, http.MethodGet, "/api/v1/extensions/history", nil, nil, nil)
		if err == nil && !asJSON {
			return printExtensionHistory(data)
		}
	case sub == "undo" && len(positional) == 2:
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions", nil, map[string]any{"action": "undo", "rev": positional[1]}, nil)
		if err == nil && !asJSON {
			return printExtensionUndo(data)
		}
	case sub == "key" && len(positional) == 3:
		ref, refErr := keyRefFromFlags(args, command)
		if refErr != nil {
			return refErr
		}
		if (ref == nil) != hasFlag(args, "--clear") {
			return errors.New(extensionsUsage)
		}
		data, _, err = client.call(ctx, http.MethodPost, "/api/v1/extensions", nil, map[string]any{"action": "key", "id": positional[1], "mount": positional[2], "key": ref, "sessionId": caller}, nil)
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

type extensionChangeView struct {
	Rev        string    `json:"rev"`
	Author     string    `json:"author"`
	Time       time.Time `json:"time"`
	Message    string    `json:"message"`
	Extensions []struct {
		ID      string   `json:"id"`
		Created bool     `json:"created"`
		Removed bool     `json:"removed"`
		Added   []string `json:"added"`
		Updated []string `json:"updated"`
		Deleted []string `json:"deleted"`
	} `json:"extensions"`
}

func printExtensionHistory(data []byte) error {
	var view struct {
		Changes []extensionChangeView `json:"changes"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return err
	}
	if len(view.Changes) == 0 {
		fmt.Println("[extensions] no history yet")
	}
	for _, change := range view.Changes {
		fmt.Printf("%s  %s  %s: %s\n", change.Rev, change.Time.Local().Format(time.DateTime), change.Author, change.Message)
		for _, files := range change.Extensions {
			what := "changed"
			switch {
			case files.Created:
				what = "created"
			case files.Removed:
				what = "removed"
			}
			fmt.Printf("  %s %s:", what, files.ID)
			for _, list := range []struct {
				mark  string
				files []string
			}{{"+", files.Added}, {"~", files.Updated}, {"-", files.Deleted}} {
				for _, file := range list.files {
					fmt.Printf(" %s%s", list.mark, file)
				}
			}
			fmt.Println()
		}
	}
	return nil
}

func printExtensionUndo(data []byte) error {
	var view struct {
		Undone  extensionChangeView `json:"undone"`
		Removed []string            `json:"removed"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return err
	}
	fmt.Printf("[extensions] undid %s (%s)\n", view.Undone.Rev, view.Undone.Message)
	if len(view.Removed) > 0 {
		fmt.Printf("[extensions] removed %s with its tables and approvals\n", strings.Join(view.Removed, ", "))
	}
	return nil
}

func printExtensionCall(data []byte) error {
	var view struct {
		Result   json.RawMessage `json:"result"`
		Ran      string          `json:"ran"`
		Reloaded bool            `json:"reloaded"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return err
	}
	if view.Reloaded {
		fmt.Println("[extensions] loaded the folder as it is now")
	}
	fmt.Printf("[extensions] %s ran ok\n", view.Ran)
	if len(view.Result) > 0 {
		fmt.Println(string(view.Result))
	}
	return nil
}

func (a *App) buildExtension(ctx context.Context, client *daemonAPIClient, message, agent string, asJSON bool) error {
	if agent == "" {
		agent = "claude"
	}
	if agent != "claude" && agent != "codex" {
		return errors.New("--agent is claude or codex")
	}
	data, _, err := client.call(ctx, http.MethodPost, "/api/v1/extensions/build", nil, map[string]any{"message": message, "surface": agent}, http.Header{"Idempotency-Key": []string{"cli-" + uuid.NewString()}, daemon.ActionActorHeader: []string{daemon.CLIActor}})
	if err != nil {
		return err
	}
	if asJSON {
		fmt.Println(string(data))
		return nil
	}
	var view struct {
		Session *surface.Session `json:"session"`
		Error   json.RawMessage  `json:"error"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return err
	}
	if view.Session == nil {
		if text := createErrorText(view.Error); text != "" {
			return errors.New(text)
		}
		return errors.New("the builder did not start")
	}
	fmt.Printf("[extensions] started %s:%s in ~/.agenthail/extensions; talk to it with agenthail send %s:%s \"...\"\n", view.Session.Surface, view.Session.ID, view.Session.Surface, view.Session.ID)
	fmt.Println("[extensions] each of its turns asks you to keep or undo what it changed")
	return nil
}
