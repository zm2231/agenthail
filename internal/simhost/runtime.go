package simhost

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type iosRuntime struct {
	Identifier string
	Version    string
}

func runtimeVersion(identifier string) string {
	i := strings.LastIndex(identifier, "iOS-")
	if i < 0 {
		return ""
	}
	return strings.ReplaceAll(identifier[i+len("iOS-"):], "-", ".")
}

func (r iosRuntime) Label() string { return "iOS " + r.Version }

func (r iosRuntime) exactly(want string) bool {
	return want == r.Identifier || want == r.Version || want == runtimeVersion(r.Identifier)
}

func (r iosRuntime) within(want string) bool {
	return strings.HasPrefix(r.Version+".", want+".") || strings.HasPrefix(runtimeVersion(r.Identifier)+".", want+".")
}

func runtimeWanted(want string) string {
	want = strings.TrimSpace(want)
	if len(want) > 4 && strings.EqualFold(want[:4], "iOS ") {
		want = strings.TrimSpace(want[4:])
	}
	return want
}

func (r iosRuntime) matches(want string) bool {
	want = runtimeWanted(want)
	return want != "" && (r.exactly(want) || r.within(want))
}

func (h *Host) iosRuntimes(ctx context.Context) ([]iosRuntime, error) {
	out, err := h.run(ctx, "xcrun", "simctl", "list", "runtimes", "--json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Runtimes []struct {
			Identifier  string `json:"identifier"`
			Version     string `json:"version"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("read simulator runtimes: %w", err)
	}
	var found []iosRuntime
	for _, r := range list.Runtimes {
		if !r.IsAvailable || !strings.Contains(r.Identifier, "iOS") {
			continue
		}
		version := r.Version
		if version == "" {
			version = runtimeVersion(r.Identifier)
		}
		found = append(found, iosRuntime{Identifier: r.Identifier, Version: version})
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no iOS Simulator runtime is installed")
	}
	return found, nil
}

// A shorter version such as 18 must name only one installed runtime.
func pickRuntime(installed []iosRuntime, want string) (iosRuntime, error) {
	want = runtimeWanted(want)
	var within []iosRuntime
	for _, r := range installed {
		if r.exactly(want) {
			return r, nil
		}
		if want != "" && r.within(want) {
			within = append(within, r)
		}
	}
	if len(within) == 1 {
		return within[0], nil
	}
	labels := make([]string, len(installed))
	for i, r := range installed {
		labels[i] = r.Version
	}
	reason := "is not installed"
	if len(within) > 1 {
		reason = "names more than one installed runtime"
	}
	return iosRuntime{}, &Error{
		Code:    "invalid",
		Message: fmt.Sprintf("iOS %s %s. Installed: %s. Agenthail never downloads a runtime; install one in Xcode > Settings > Components.", want, reason, strings.Join(labels, ", ")),
		Detail:  map[string]any{"installed": labels},
	}
}

func (k Kind) nameOn(r iosRuntime) string {
	return k.Name + " (" + r.Label() + ")"
}

func agenthailKind(name string) (string, bool) {
	if strings.HasSuffix(name, " (slimming)") {
		return "", false
	}
	for kindName, kind := range Kinds {
		if name == kind.Name || (strings.HasPrefix(name, kind.Name+" (iOS ") && strings.HasSuffix(name, ")")) {
			return kindName, true
		}
	}
	return "", false
}

type DeviceView struct {
	Kind    string `json:"kind"`
	Device  string `json:"device"`
	Runtime string `json:"runtime,omitempty"`
	UDID    string `json:"udid"`
	State   string `json:"state,omitempty"`
}

func agenthailDevices(all []device) []DeviceView {
	var out []DeviceView
	for _, d := range all {
		kindName, ok := agenthailKind(d.Name)
		if !ok {
			continue
		}
		view := DeviceView{Kind: kindName, Device: d.Name, UDID: d.UDID, State: d.State}
		if version := runtimeVersion(d.Runtime); version != "" {
			view.Runtime = "iOS " + version
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}
