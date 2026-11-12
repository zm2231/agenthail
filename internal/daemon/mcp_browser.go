package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/browserhost"
)

const browserHowTo = "Drive this tab with snapshot, then act on a ref from it (kind click, type, select, hover, drag, press or upload); refs last until the next snapshot. snapshot interactive lists only what you can act on; ref or selector narrows it to one part, depth to fewer levels. To read, use text (scroll loads a lazy page to its end first) and links. A result too long to return whole comes back cut, with the file that holds all of it; a snapshot file's refs work too. scroll moves the page or the container that scrolls (to top or bottom, by pixels, or a ref into view) and says whether it moved. dismiss closes a cookie banner or modal by Escape or its Close button. screenshot saves a PNG of the view, the whole page (fullPage) or one ref, to path if you give one. Use wait for text, a ref, a URL or network idle. A dialog blocks the page until you answer it with dialog. Tabs stay open between turns; close the ones you opened before you reply unless the person uses them."

const browserTextLimit = mcpOutputLimit - 512

type mcpBrowserInput struct {
	Action      string   `json:"action" jsonschema:"servers, open, navigate, snapshot, text, links, scroll, dismiss, screenshot, act, wait, dialog, resize, list or close."`
	Tab         string   `json:"tab,omitempty" jsonschema:"Tab id from open or list."`
	URL         string   `json:"url,omitempty" jsonschema:"open, navigate; wait: a URL part to reach."`
	FullPage    bool     `json:"fullPage,omitempty" jsonschema:"screenshot: the whole scrollable page instead of the view."`
	Path        string   `json:"path,omitempty" jsonschema:"screenshot: a new absolute .png path in your folders to save to."`
	Kind        string   `json:"kind,omitempty" jsonschema:"act: click, type, select, hover, drag, press or upload."`
	Ref         string   `json:"ref,omitempty" jsonschema:"A ref from the latest snapshot: the element, or for snapshot, text and links that part; scroll: into view, or inside it with to or by."`
	Selector    string   `json:"selector,omitempty" jsonschema:"snapshot, text, links: a CSS selector instead of ref."`
	Interactive bool     `json:"interactive,omitempty" jsonschema:"snapshot: only elements you can act on."`
	Depth       *int     `json:"depth,omitempty" jsonschema:"snapshot: at most this many levels."`
	Scroll      bool     `json:"scroll,omitempty" jsonschema:"text: scroll to the end first so lazy parts load."`
	To          string   `json:"to,omitempty" jsonschema:"scroll: top or bottom."`
	By          *int     `json:"by,omitempty" jsonschema:"scroll: CSS pixels, negative for up."`
	Target      string   `json:"target,omitempty" jsonschema:"act drag: the ref to drop on."`
	Text        string   `json:"text,omitempty" jsonschema:"act type: text to enter; wait: text to appear; dialog: a prompt's answer."`
	Key         string   `json:"key,omitempty" jsonschema:"act press: a key such as Enter."`
	Values      []string `json:"values,omitempty" jsonschema:"act select: option values or labels."`
	Files       []string `json:"files,omitempty" jsonschema:"act upload: file paths on this host."`
	Idle        bool     `json:"idle,omitempty" jsonschema:"wait, scroll: for network idle."`
	Seconds     int      `json:"seconds,omitempty" jsonschema:"wait: at most 30, the default."`
	Accept      bool     `json:"accept,omitempty" jsonschema:"dialog: true accepts, false dismisses."`
	Width       int      `json:"width,omitempty" jsonschema:"resize: viewport width in CSS pixels."`
	Height      int      `json:"height,omitempty" jsonschema:"resize: viewport height in CSS pixels."`
}

func (d *Daemon) mcpBrowser(ctx context.Context, caller *mcpCaller, input mcpBrowserInput) (any, error) {
	if !caller.has("browser.drive") {
		return nil, refuse("capability_missing", "Driving the browser needs browser.drive.")
	}
	if input.Action == "servers" {
		return d.browserServers(caller)
	}
	if d.browserHost == nil {
		return nil, refuse("unavailable", "This daemon runs no browser host.")
	}
	params, timeout, err := browserCall(caller.session.ID, input)
	if err != nil {
		return nil, err
	}
	var save string
	if input.Action == "screenshot" && input.Path != "" {
		if save, err = screenshotPath(caller, input.Path); err != nil {
			return nil, err
		}
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
	if file, _ := out["file"].(string); save != "" && file != "" {
		if err := saveScreenshot(file, save); err != nil {
			return nil, err
		}
		out["path"] = save
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
		for key, value := range map[string]any{"limit": browserTextLimit, "interactive": input.Interactive, "ref": input.Ref, "selector": input.Selector, "depth": input.Depth} {
			params[key] = value
		}
	case "text":
		for key, value := range map[string]any{"limit": browserTextLimit, "ref": input.Ref, "selector": input.Selector, "scroll": input.Scroll} {
			params[key] = value
		}
	case "links":
		params["limit"], params["ref"], params["selector"] = browserTextLimit, input.Ref, input.Selector
	case "scroll":
		params["to"], params["by"], params["ref"], params["idle"] = input.To, input.By, input.Ref, input.Idle
	case "screenshot":
		params["fullPage"], params["ref"] = input.FullPage, input.Ref
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
	case "list", "close", "dismiss":
	default:
		return nil, 0, refuse("invalid", "action must be servers, open, navigate, snapshot, text, links, scroll, dismiss, screenshot, act, wait, dialog, resize, list or close.")
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
