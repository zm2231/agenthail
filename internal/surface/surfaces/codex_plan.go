package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const codexPlanFirstReadBytes = int64(4 << 20)

type codexPlanState struct {
	identity string
	offset   int64
	plan     *surface.Plan
	read     bool
}

type codexPlanRecord struct {
	Type    string `json:"type"`
	Payload struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Input     string `json:"input"`
	} `json:"payload"`
}

// plan reads the newest update_plan in a rollout: the tail on first sight,
// then only what was appended since.
func (c *Codex) plan(ctx context.Context, path string, info os.FileInfo) *surface.Plan {
	c.planMu.Lock()
	defer c.planMu.Unlock()
	if c.planState == nil {
		c.planState = map[string]*codexPlanState{}
	}
	identity := transcriptFileIdentity(info)
	state := c.planState[path]
	if state != nil && identity != "" && state.identity == identity && info.Size() == state.offset {
		state.read = true
		return state.plan
	}
	if state == nil || identity == "" || state.identity != identity || info.Size() < state.offset {
		state = &codexPlanState{identity: identity}
		lines, offset, err := readRecentJSONLLines(ctx, path, 0, codexPlanFirstReadBytes, maxCodexTranscriptRecordBytes)
		if err != nil {
			delete(c.planState, path)
			return nil
		}
		for _, line := range lines {
			state.apply(line)
		}
		state.offset = offset
		c.planState[path] = state
	}
	offset, err := scanAppendedJSONL(ctx, path, state.offset, maxCodexTranscriptRecordBytes, func(line []byte) error {
		state.apply(line)
		return nil
	})
	if err == nil {
		state.offset = offset
	}
	state.read = true
	return state.plan
}

// forgetUnreadPlans drops the plans no read has asked for since the last
// complete listing, which reads every rollout it lists.
func (c *Codex) forgetUnreadPlans() {
	c.planMu.Lock()
	defer c.planMu.Unlock()
	for path, state := range c.planState {
		if !state.read {
			delete(c.planState, path)
			continue
		}
		state.read = false
	}
}

func (s *codexPlanState) apply(line []byte) {
	if !strings.Contains(string(line), "update_plan") {
		return
	}
	var record codexPlanRecord
	if json.Unmarshal(line, &record) != nil || record.Type != "response_item" {
		return
	}
	var arguments string
	switch {
	case record.Payload.Type == "function_call" && record.Payload.Name == "update_plan":
		arguments = record.Payload.Arguments
	case record.Payload.Type == "custom_tool_call":
		literal, found := lastCallLiteral(record.Payload.Input, "tools.update_plan(")
		if !found {
			return
		}
		converted, ok := jsLiteralJSON(literal)
		if !ok {
			s.plan = nil
			return
		}
		arguments = converted
	default:
		return
	}
	var update struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if json.Unmarshal([]byte(arguments), &update) != nil {
		s.plan = nil
		return
	}
	plan := &surface.Plan{}
	for _, step := range update.Plan {
		plan.Total++
		switch step.Status {
		case "completed":
			plan.Done++
		case "in_progress":
			if plan.Current == "" {
				plan.Current = step.Step
			}
		}
	}
	if plan.Total == 0 {
		plan = nil
	}
	s.plan = plan
}

// lastCallLiteral finds the object literal passed to the last call of fn in
// code-mode source, as tools.update_plan({plan: [...]}).
func lastCallLiteral(source, fn string) (string, bool) {
	at := strings.LastIndex(source, fn)
	if at < 0 {
		return "", false
	}
	rest := strings.TrimLeft(source[at+len(fn):], " \t\r\n")
	if !strings.HasPrefix(rest, "{") {
		return "", false
	}
	depth := 0
	var quote byte
	for index := 0; index < len(rest); index++ {
		char := rest[index]
		if quote != 0 {
			switch char {
			case '\\':
				index++
			case quote:
				quote = 0
			}
			continue
		}
		switch char {
		case '"', '\'', '`':
			quote = char
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return rest[:index+1], true
			}
		}
	}
	return "", false
}

// jsLiteralJSON turns a plain JavaScript object literal into JSON: bare keys
// are quoted, single-quoted strings become double-quoted, and trailing commas
// go. Anything else, such as a variable or a template string, is refused.
func jsLiteralJSON(literal string) (string, bool) {
	var out strings.Builder
	for index := 0; index < len(literal); {
		char := literal[index]
		switch {
		case char == '"':
			end := jsStringEnd(literal, index)
			if end < 0 {
				return "", false
			}
			out.WriteString(literal[index : end+1])
			index = end + 1
		case char == '\'':
			end := jsStringEnd(literal, index)
			if end < 0 {
				return "", false
			}
			body := strings.ReplaceAll(literal[index+1:end], `\'`, `'`)
			encoded, _ := json.Marshal(body)
			out.Write(encoded)
			index = end + 1
		case char == '`':
			return "", false
		case char == ',':
			next := index + 1
			for next < len(literal) && strings.ContainsRune(" \t\r\n", rune(literal[next])) {
				next++
			}
			if next < len(literal) && (literal[next] == '}' || literal[next] == ']') {
				index = next
				continue
			}
			out.WriteByte(char)
			index++
		case char == '_' || char == '$' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z':
			end := index
			for end < len(literal) && (literal[end] == '_' || literal[end] == '$' || literal[end] >= 'a' && literal[end] <= 'z' || literal[end] >= 'A' && literal[end] <= 'Z' || literal[end] >= '0' && literal[end] <= '9') {
				end++
			}
			word := literal[index:end]
			after := end
			for after < len(literal) && strings.ContainsRune(" \t\r\n", rune(literal[after])) {
				after++
			}
			switch {
			case after < len(literal) && literal[after] == ':':
				encoded, _ := json.Marshal(word)
				out.Write(encoded)
			case word == "true" || word == "false" || word == "null":
				out.WriteString(word)
			default:
				return "", false
			}
			index = end
		default:
			out.WriteByte(char)
			index++
		}
	}
	return out.String(), true
}

func jsStringEnd(source string, start int) int {
	quote := source[start]
	for index := start + 1; index < len(source); index++ {
		switch source[index] {
		case '\\':
			index++
		case quote:
			return index
		}
	}
	return -1
}
