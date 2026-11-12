package simhost

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"strconv"
	"strings"
)

func (h *Host) describe(ctx context.Context, owner string, l *lease, limit int) (map[string]any, error) {
	raw, err := h.run(ctx, "axe", "describe-ui", "--udid", l.udid)
	if err != nil {
		return nil, err
	}
	lines, refs, err := describeLines(raw)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	l.refs = refs
	h.mu.Unlock()
	whole := strings.Join(lines, "\n") + "\n"
	file, err := h.writeOut(owner, "describe.txt", []byte(whole))
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = len(whole)
	}
	text, cut := head(lines, limit)
	result := map[string]any{"tree": text, "truncated": cut, "file": file, "elements": len(refs)}
	if cut {
		result["full"] = "The whole tree is in file; its refs work until the next describe."
	}
	return result, nil
}

func (h *Host) at(l *lease, ref string, x, y *float64) (point, error) {
	if ref != "" {
		h.mu.Lock()
		p, ok := l.refs[ref]
		h.mu.Unlock()
		if !ok {
			return point{}, refuse("not_found", "No ref %s in the latest describe; describe again.", ref)
		}
		return p, nil
	}
	if x == nil || y == nil {
		return point{}, refuse("invalid", "Give a ref, or x and y.")
	}
	return point{X: *x, Y: *y}, nil
}

func coord(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func (h *Host) tap(ctx context.Context, l *lease, req Request) (map[string]any, error) {
	p, err := h.at(l, req.Ref, req.X, req.Y)
	if err != nil {
		return nil, err
	}
	_, err = h.run(ctx, "axe", "tap", "-x", coord(p.X), "-y", coord(p.Y), "--udid", l.udid)
	return map[string]any{"tapped": map[string]float64{"x": p.X, "y": p.Y}}, err
}

func (h *Host) swipe(ctx context.Context, l *lease, req Request) (map[string]any, error) {
	from, err := h.at(l, req.Ref, req.X, req.Y)
	if err != nil {
		return nil, err
	}
	if req.ToX == nil || req.ToY == nil {
		return nil, refuse("invalid", "toX and toY are required.")
	}
	seconds := req.Seconds
	if seconds <= 0 || seconds > 5 {
		seconds = 0.6
	}
	_, err = h.run(ctx, "axe", "swipe", "--start-x", coord(from.X), "--start-y", coord(from.Y), "--end-x", coord(*req.ToX), "--end-y", coord(*req.ToY), "--duration", coord(seconds), "--udid", l.udid)
	return map[string]any{"swiped": true}, err
}

func (h *Host) screenshot(ctx context.Context, owner string, l *lease) (map[string]any, error) {
	path, err := h.outFile(owner, "screenshot.png")
	if err != nil {
		return nil, err
	}
	if _, err := h.run(ctx, "xcrun", "simctl", "io", l.udid, "screenshot", "--type=png", path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	h.prune(owner, path)
	result := map[string]any{"file": path, "bytes": len(data)}
	if config, err := png.DecodeConfig(bytes.NewReader(data)); err == nil {
		result["width"], result["height"] = config.Width, config.Height
	}
	return result, nil
}
