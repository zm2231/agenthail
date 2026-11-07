package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/browserhost"
)

const browserHowTo = "Drive this tab with snapshot, then act on a ref from it (kind click, type, select, hover, drag, press, scroll or upload); refs last until the next snapshot. Use wait for text, a ref, a URL or network idle. A dialog blocks the page until you answer it with dialog. Tabs stay open between turns; close the ones you opened before you reply unless the person uses them."

type mcpBrowserInput struct {
	Action       string   `json:"action" jsonschema:"open, navigate, snapshot, act, wait, dialog, resize, list or close."`
	Tab          string   `json:"tab,omitempty" jsonschema:"Tab id from open or list."`
	URL          string   `json:"url,omitempty" jsonschema:"open, navigate; wait: a URL part to reach."`
	IncludeImage bool     `json:"includeImage,omitempty" jsonschema:"snapshot: also save a screenshot; its path is returned."`
	Kind         string   `json:"kind,omitempty" jsonschema:"act: click, type, select, hover, drag, press, scroll or upload."`
	Ref          string   `json:"ref,omitempty" jsonschema:"act, wait: an element ref from the latest snapshot."`
	Target       string   `json:"target,omitempty" jsonschema:"act drag: the ref to drop on."`
	Text         string   `json:"text,omitempty" jsonschema:"act type: text to enter; wait: text to appear; dialog: a prompt's answer."`
	Key          string   `json:"key,omitempty" jsonschema:"act press: a key such as Enter."`
	Values       []string `json:"values,omitempty" jsonschema:"act select: option values or labels."`
	Files        []string `json:"files,omitempty" jsonschema:"act upload: file paths on this host."`
	Idle         bool     `json:"idle,omitempty" jsonschema:"wait: for network idle."`
	Seconds      int      `json:"seconds,omitempty" jsonschema:"wait: at most 30, the default."`
	Accept       bool     `json:"accept,omitempty" jsonschema:"dialog: true accepts, false dismisses."`
	Width        int      `json:"width,omitempty" jsonschema:"resize: viewport width in CSS pixels."`
	Height       int      `json:"height,omitempty" jsonschema:"resize: viewport height in CSS pixels."`
}

func (d *Daemon) mcpBrowser(ctx context.Context, caller *mcpCaller, input mcpBrowserInput) (any, error) {
	if !caller.has("browser.drive") {
		return nil, refuse("capability_missing", "Driving the browser needs browser.drive.")
	}
	if d.browserHost == nil {
		return nil, refuse("unavailable", "This daemon runs no browser host.")
	}
	params, timeout, err := browserCall(caller.session.ID, input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out map[string]any
	if err := d.browserHost.Client().Call(ctx, "tabs."+input.Action, params, &out); err != nil {
		return nil, d.browserRefusal(err)
	}
	if input.Action == "open" || input.Action == "close" {
		d.browserHost.Refresh(ctx)
	}
	if input.Action == "open" {
		out["next"] = browserHowTo
	}
	return out, nil
}

func browserCall(owner string, input mcpBrowserInput) (map[string]any, time.Duration, error) {
	params := map[string]any{"owner": owner}
	if input.Action != "open" && input.Action != "list" {
		if input.Tab == "" {
			return nil, 0, refuse("invalid", "tab is required: use the id from open or list.")
		}
		params["tab"] = input.Tab
	}
	timeout := time.Minute
	switch input.Action {
	case "open":
		params["url"] = input.URL
		timeout = 11 * time.Minute
	case "navigate":
		if input.URL == "" {
			return nil, 0, refuse("invalid", "url is required.")
		}
		params["url"] = input.URL
	case "snapshot":
		params["includeImage"] = input.IncludeImage
	case "act":
		for key, value := range map[string]any{"kind": input.Kind, "ref": input.Ref, "target": input.Target, "text": input.Text, "key": input.Key, "values": input.Values, "files": input.Files} {
			params[key] = value
		}
	case "wait":
		seconds := min(max(input.Seconds, 0), 30)
		if seconds == 0 {
			seconds = 30
		}
		for key, value := range map[string]any{"text": input.Text, "ref": input.Ref, "url": input.URL, "idle": input.Idle, "timeoutMs": seconds * 1000} {
			params[key] = value
		}
	case "dialog":
		params["accept"] = input.Accept
		params["text"] = input.Text
	case "resize":
		params["width"], params["height"] = input.Width, input.Height
	case "list", "close":
	default:
		return nil, 0, refuse("invalid", "action must be open, navigate, snapshot, act, wait, dialog, resize, list or close.")
	}
	return params, timeout, nil
}

func (d *Daemon) browserRefusal(err error) error {
	var hostErr *browserhost.Error
	switch {
	case errors.As(err, &hostErr) && hostErr.Code == "missing_libraries":
		return refuse(hostErr.Code, hostErr.Message+". The person installs them with: "+d.browserHost.Status().Install)
	case errors.As(err, &hostErr):
		return refuse(hostErr.Code, hostErr.Message)
	case errors.Is(err, browserhost.ErrNotRunning):
		return &mcpRefusal{Code: "unavailable", Message: "The browser host is starting or restarting; try again in a moment. Tabs from before a restart are gone.", Retryable: true}
	default:
		return err
	}
}
