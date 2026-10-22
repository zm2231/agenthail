package surfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const codexContextHookTimeout = 10

var codexContextEvents = []struct{ event, key string }{
	{"SessionStart", "session_start"},
	{"UserPromptSubmit", "user_prompt_submit"},
}

// AgenthailExecutable is the running agenthail binary, or "" when this
// process is not one, such as a test binary, which must never run as a hook.
func AgenthailExecutable() string {
	executable, err := os.Executable()
	if err != nil || filepath.Base(executable) != "agenthail" {
		return ""
	}
	return executable
}

func CodexContextHookCommand(executable string) string {
	return "exec '" + strings.ReplaceAll(executable, "'", `'\''`) + "' hook session-context --surface codex"
}

// CodexContextHooks is the hooks table that has Codex run the session-context
// hook, and the trust entries that let it run, for a config whose hooks Codex
// keys by source. Codex runs a hook only once its trusted_hash matches the
// hash Codex computes from the hook's declaration.
func CodexContextHooks(command, source string) (hooks map[string]any, state map[string]any) {
	hooks, state = map[string]any{}, map[string]any{}
	for _, e := range codexContextEvents {
		hooks[e.event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": codexContextHookTimeout}}}}
		state[source+":"+e.key+":0:0"] = map[string]any{"trusted_hash": codexHookHash(e.key, command, codexContextHookTimeout)}
	}
	return hooks, state
}

var agenthailExecutable = AgenthailExecutable

func codexThreadContextConfig() map[string]any {
	executable := agenthailExecutable()
	if executable == "" {
		return nil
	}
	hooks, state := CodexContextHooks(CodexContextHookCommand(executable), "/<session-flags>/config.toml")
	hooks["state"] = state
	return map[string]any{"hooks": hooks}
}

// codexHookHash is Codex's declaration hash for a command hook with no
// matcher: sha256 of the sorted compact JSON of the event and its one
// normalized handler (codex-rs hooks hook_hash and config version_for_toml),
// written as serde_json writes it.
func codexHookHash(eventKey, command string, timeout int) string {
	identity := `{"event_name":` + serdeJSONString(eventKey) + `,"hooks":[{"async":false,"command":` + serdeJSONString(command) + `,"timeout":` + strconv.Itoa(timeout) + `,"type":"command"}]}`
	sum := sha256.Sum256([]byte(identity))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// serdeJSONString escapes only what serde_json escapes: the quote, the
// backslash and control characters. encoding/json also escapes U+2028 and
// U+2029, which would change the hash.
func serdeJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
