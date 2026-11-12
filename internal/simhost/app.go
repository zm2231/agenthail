package simhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (h *Host) install(ctx context.Context, owner string, l *lease, app string) (map[string]any, error) {
	if !filepath.IsAbs(app) || filepath.Ext(filepath.Clean(app)) != ".app" {
		return nil, refuse("invalid", "app must be the absolute path of a .app.")
	}
	staged, cleanup, err := h.stageApp(owner, filepath.Clean(app))
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if _, err := h.run(ctx, "xcrun", "simctl", "install", l.udid, staged); err != nil {
		return nil, err
	}
	return map[string]any{"installed": app}, nil
}

func (h *Host) launch(ctx context.Context, l *lease, req Request) (map[string]any, error) {
	if !bundlePattern.MatchString(req.BundleID) {
		return nil, refuse("invalid", "bundleId is required.")
	}
	keys := make([]string, 0, len(req.Env))
	for key := range req.Env {
		if !envPattern.MatchString(key) {
			return nil, refuse("invalid", "env names are letters, digits and _.")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, "SIMCTL_CHILD_"+key+"="+req.Env[key])
	}
	args := append([]string{"simctl", "launch", "--terminate-running-process", l.udid, req.BundleID}, req.Args...)
	out, err := h.exec(ctx, Command{Name: "xcrun", Args: args, Env: env})
	if err != nil {
		return nil, err
	}
	l.launched[req.BundleID] = true
	result := map[string]any{"launched": req.BundleID}
	if fields := strings.Fields(string(out)); len(fields) > 0 {
		if pid, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			result["pid"] = pid
		}
	}
	return result, nil
}

func (h *Host) push(ctx context.Context, owner string, l *lease, req Request) (map[string]any, error) {
	if !bundlePattern.MatchString(req.BundleID) {
		return nil, refuse("invalid", "bundleId is required.")
	}
	var payload map[string]any
	if json.Unmarshal(req.Payload, &payload) != nil || payload["aps"] == nil {
		return nil, refuse("invalid", "payload must be a JSON object with aps.")
	}
	path, err := h.writeOut(owner, "push.json", req.Payload)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	_, err = h.run(ctx, "xcrun", "simctl", "push", l.udid, req.BundleID, path)
	return map[string]any{"pushed": req.BundleID}, err
}

func (h *Host) location(ctx context.Context, l *lease, req Request) (map[string]any, error) {
	if req.Latitude == nil && req.Longitude == nil {
		_, err := h.run(ctx, "xcrun", "simctl", "location", l.udid, "clear")
		return map[string]any{"location": "cleared"}, err
	}
	if req.Latitude == nil || req.Longitude == nil || *req.Latitude < -90 || *req.Latitude > 90 || *req.Longitude < -180 || *req.Longitude > 180 {
		return nil, refuse("invalid", "Give latitude and longitude, or neither to clear.")
	}
	at := coord(*req.Latitude) + "," + coord(*req.Longitude)
	_, err := h.run(ctx, "xcrun", "simctl", "location", l.udid, "set", at)
	return map[string]any{"location": at}, err
}

var executablePattern = regexp.MustCompile(`CFBundleExecutable = "?([^";\n]+)"?;`)

func (h *Host) logs(ctx context.Context, owner string, l *lease, req Request) (map[string]any, error) {
	if !bundlePattern.MatchString(req.BundleID) {
		return nil, refuse("invalid", "bundleId is required.")
	}
	info, err := h.run(ctx, "xcrun", "simctl", "appinfo", l.udid, req.BundleID)
	if err != nil {
		return nil, err
	}
	match := executablePattern.FindSubmatch(info)
	if match == nil {
		return nil, refuse("not_found", "%s is not installed on this simulator.", req.BundleID)
	}
	seconds := int(req.Seconds)
	if seconds <= 0 || seconds > 300 {
		seconds = 60
	}
	predicate := fmt.Sprintf("process == %s", strconv.Quote(strings.TrimSpace(string(match[1]))))
	out, err := h.run(ctx, "xcrun", "simctl", "spawn", l.udid, "log", "show", "--last", strconv.Itoa(seconds)+"s", "--style", "compact", "--predicate", predicate)
	if err != nil {
		return nil, err
	}
	file, err := h.writeOut(owner, "logs.txt", out)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	limit := req.Limit
	if limit <= 0 {
		limit = len(out) + 1
	}
	start := 0
	total := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if total+len(lines[i])+1 > limit {
			start = i + 1
			break
		}
		total += len(lines[i]) + 1
	}
	return map[string]any{"log": strings.Join(lines[start:], "\n"), "truncated": start > 0, "file": file}, nil
}
