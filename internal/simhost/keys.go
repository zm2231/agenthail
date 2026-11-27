package simhost

import (
	"context"
	"strconv"
	"strings"
)

// maxKeys is AXe's own limit on one key-sequence.
const maxKeys = 100

func (h *Host) press(ctx context.Context, l *lease, req Request) (map[string]any, error) {
	if (req.Key == nil) == (len(req.Keys) == 0) {
		return nil, refuse("invalid", "Give key, or keys for a sequence.")
	}
	if len(req.Keys) > 0 {
		if len(req.Modifiers) > 0 {
			return nil, refuse("invalid", "modifiers hold for one key; press a sequence without them.")
		}
		if len(req.Keys) > maxKeys {
			return nil, refuse("invalid", "keys takes at most %d keycodes.", maxKeys)
		}
		for _, code := range req.Keys {
			if !keycode(code) {
				return nil, refuse("invalid", "Keycode %d is not 0 to 255.", code)
			}
		}
		_, err := h.run(ctx, "axe", "key-sequence", "--keycodes", joinCodes(req.Keys), "--udid", l.udid)
		return map[string]any{"pressed": req.Keys}, err
	}
	key := *req.Key
	if !keycode(key) {
		return nil, refuse("invalid", "Keycode %d is not 0 to 255.", key)
	}
	if len(req.Modifiers) == 0 {
		_, err := h.run(ctx, "axe", "key", strconv.Itoa(key), "--udid", l.udid)
		return map[string]any{"pressed": []int{key}}, err
	}
	seen := map[int]bool{}
	for _, m := range req.Modifiers {
		if m < 224 || m > 231 || seen[m] || m == key {
			return nil, refuse("invalid", "modifiers are distinct HID modifier keys, 224 to 231, other than key.")
		}
		seen[m] = true
	}
	_, err := h.run(ctx, "axe", "key-combo", "--modifiers", joinCodes(req.Modifiers), "--key", strconv.Itoa(key), "--udid", l.udid)
	return map[string]any{"pressed": []int{key}, "modifiers": req.Modifiers}, err
}

func keycode(code int) bool { return code >= 0 && code <= 255 }

func joinCodes(codes []int) string {
	parts := make([]string, len(codes))
	for i, code := range codes {
		parts[i] = strconv.Itoa(code)
	}
	return strings.Join(parts, ",")
}
