package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
)

const (
	claudeCallReadBackFirst int64 = 64 << 10
	claudeCallReadBackMost  int64 = 4 << 20
)

type claudeCallSite struct {
	name   string
	offset int64
}

// claudeToolCalls names the tool of a call id, from the tool_use records a
// build has seen or, for a call before them, by reading back through the
// transcript from the result. A call it can't find has no name.
type claudeToolCalls struct {
	path  string
	calls map[string]claudeCallSite
}

func newClaudeToolCalls(path string) *claudeToolCalls {
	return &claudeToolCalls{path: path, calls: map[string]claudeCallSite{}}
}

func (c *claudeToolCalls) observe(record map[string]any, offset int64) {
	if c == nil || str(record, "type") != "assistant" {
		return
	}
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		if block, _ := raw.(map[string]any); str(block, "type") == "tool_use" && str(block, "id") != "" {
			c.calls[str(block, "id")] = claudeCallSite{name: str(block, "name"), offset: offset}
		}
	}
}

func (c *claudeToolCalls) name(ctx context.Context, callID string, before int64) (string, error) {
	if c == nil || callID == "" {
		return "", nil
	}
	if call, ok := c.calls[callID]; ok && call.offset < before {
		return call.name, nil
	}
	name, offset, err := readBackToolCall(ctx, c.path, callID, before)
	if err != nil {
		return "", err
	}
	if name != "" {
		c.calls[callID] = claudeCallSite{name: name, offset: offset}
	}
	return name, nil
}

func readBackToolCall(ctx context.Context, path, callID string, before int64) (string, int64, error) {
	if path == "" || before <= 0 {
		return "", 0, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, nil
	}
	defer file.Close()
	needle := []byte(`"` + callID + `"`)
	for window := claudeCallReadBackFirst; ; window *= 2 {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		window = min(window, claudeCallReadBackMost)
		start := max(0, before-window)
		data := make([]byte, before-start)
		if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
			return "", 0, nil
		}
		if start > 0 {
			cut := bytes.IndexByte(data, '\n')
			if cut < 0 {
				data = nil
			} else {
				data, start = data[cut+1:], start+int64(cut+1)
			}
		}
		lines := bytes.SplitAfter(data, []byte("\n"))
		at := before
		for i := len(lines) - 1; i >= 0; i-- {
			at -= int64(len(lines[i]))
			if !bytes.Contains(lines[i], needle) {
				continue
			}
			var record map[string]any
			if json.Unmarshal(lines[i], &record) != nil {
				continue
			}
			found := newClaudeToolCalls("")
			found.observe(record, at)
			if call, ok := found.calls[callID]; ok {
				return call.name, at, nil
			}
		}
		if start == 0 || window == claudeCallReadBackMost {
			return "", 0, nil
		}
	}
}
