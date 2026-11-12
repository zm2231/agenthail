package simhost

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

type Request struct {
	Action    string            `json:"action"`
	Kind      string            `json:"kind,omitempty"`
	App       string            `json:"app,omitempty"`
	BundleID  string            `json:"bundleId,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Ref       string            `json:"ref,omitempty"`
	X         *float64          `json:"x,omitempty"`
	Y         *float64          `json:"y,omitempty"`
	ToX       *float64          `json:"toX,omitempty"`
	ToY       *float64          `json:"toY,omitempty"`
	Seconds   float64           `json:"seconds,omitempty"`
	Text      string            `json:"text,omitempty"`
	Button    string            `json:"button,omitempty"`
	Payload   json.RawMessage   `json:"payload,omitempty"`
	Latitude  *float64          `json:"latitude,omitempty"`
	Longitude *float64          `json:"longitude,omitempty"`
	Limit     int               `json:"limit,omitempty"`
}

var Actions = []string{"list", "acquire", "renew", "release", "install", "launch", "terminate", "openUrl", "describe", "tap", "type", "swipe", "button", "screenshot", "push", "location", "logs"}

var buttons = map[string]bool{"home": true, "lock": true, "side-button": true}

var bundlePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,254}$`)

func (h *Host) Call(ctx context.Context, owner string, req Request) (map[string]any, error) {
	switch req.Action {
	case "list":
		return h.list(ctx, owner)
	case "acquire":
		view, err := h.acquire(ctx, owner, req.Kind)
		if err != nil {
			return nil, err
		}
		return map[string]any{"lease": view, "next": HowTo}, nil
	case "renew":
		view, err := h.renew(owner, req.Kind)
		return map[string]any{"lease": view}, err
	case "release":
		return map[string]any{"released": h.Release(owner, req.Kind)}, nil
	}
	l, err := h.held(owner, req.Kind)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return nil, refuse("not_held", "Your lease of that simulator has ended; acquire it again.")
	}
	switch req.Action {
	case "install":
		return h.install(ctx, owner, l, req.App)
	case "launch":
		return h.launch(ctx, l, req)
	case "terminate":
		if !bundlePattern.MatchString(req.BundleID) {
			return nil, refuse("invalid", "bundleId is required.")
		}
		_, err := h.run(ctx, "xcrun", "simctl", "terminate", l.udid, req.BundleID)
		return map[string]any{"terminated": req.BundleID}, err
	case "openUrl":
		if req.URL == "" || !strings.Contains(req.URL, ":") {
			return nil, refuse("invalid", "url is required, with its scheme.")
		}
		_, err := h.run(ctx, "xcrun", "simctl", "openurl", l.udid, req.URL)
		return map[string]any{"opened": req.URL}, err
	case "describe":
		return h.describe(ctx, owner, l, req.Limit)
	case "tap":
		return h.tap(ctx, l, req)
	case "type":
		if req.Text == "" {
			return nil, refuse("invalid", "text is required.")
		}
		_, err := h.exec(ctx, Command{Name: "axe", Args: []string{"type", "--stdin", "--udid", l.udid}, Stdin: []byte(req.Text)})
		return map[string]any{"typed": len([]rune(req.Text))}, err
	case "swipe":
		return h.swipe(ctx, l, req)
	case "button":
		if !buttons[req.Button] {
			return nil, refuse("invalid", "button must be home, lock or side-button.")
		}
		_, err := h.run(ctx, "axe", "button", req.Button, "--udid", l.udid)
		return map[string]any{"pressed": req.Button}, err
	case "screenshot":
		return h.screenshot(ctx, owner, l)
	case "push":
		return h.push(ctx, owner, l, req)
	case "location":
		return h.location(ctx, l, req)
	case "logs":
		return h.logs(ctx, owner, l, req)
	}
	return nil, refuse("invalid", "action must be %s.", strings.Join(Actions, ", "))
}

const HowTo = "Read the screen with describe, then tap a ref from it (refs last until the next describe), or tap, swipe by x,y in points. type enters text into the focused field; button presses home, lock or side-button. install takes a .app you copied into outDir; launch and terminate take its bundleId; openUrl opens a link or deep link. screenshot saves a PNG; logs reads an app's recent log. The lease ends after 10 minutes unused or at endsAt; renew moves endsAt on. release it when you are done."

func (h *Host) list(ctx context.Context, owner string) (map[string]any, error) {
	all, err := h.devices(ctx)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	kinds := make([]string, 0, len(Kinds))
	for name := range Kinds {
		kinds = append(kinds, name)
	}
	sort.Strings(kinds)
	var out []map[string]any
	for _, name := range kinds {
		kind := Kinds[name]
		entry := map[string]any{"kind": name, "device": kind.Name, "exists": false, "holder": ""}
		for _, d := range all {
			if d.Name == kind.Name {
				entry["exists"], entry["state"], entry["udid"] = true, d.State, d.UDID
				if l := h.leases[d.UDID]; l != nil {
					entry["holder"] = map[bool]string{true: "you", false: "another session"}[l.owner == owner]
					if l.owner == owner {
						entry["lease"] = h.view(l)
					}
				}
				break
			}
		}
		out = append(out, entry)
	}
	return map[string]any{"simulators": out, "outDir": h.ownerDir(owner)}, nil
}
