package surface

import "strings"

// codexContextFragments mirrors the markers Codex itself uses to tell the
// context it injects into a user-role message (AGENTS.md, the environment,
// skills, shell commands it ran for the user, subagent notices) from text a
// person typed.
var codexContextFragments = []struct{ open, close string }{
	{"# AGENTS.md instructions", "</INSTRUCTIONS>"},
	{"<environment_context>", "</environment_context>"},
	{"<user_instructions>", "</user_instructions>"},
	{"<skill>", "</skill>"},
	{"<user_shell_command>", "</user_shell_command>"},
	{"<subagent_notification>", "</subagent_notification>"},
	{"<image_resize_notice>", "</image_resize_notice>"},
	{"<codex_internal_context", "</codex_internal_context>"},
	{"<external_codex_apps_open_page>", "</external_codex_apps_open_page>"},
	{"<recommended_plugins>", "</recommended_plugins>"},
}

// codexRequestMarker is how Codex appends a person's request to the
// AGENTS.md instructions in one block; such a block is a prompt.
const codexRequestMarker = "## My request:"

// IsCodexInjectedContext reports whether text consists only of Codex's
// injected context fragments, one after another, as Codex writes them in
// one block or as the blocks of one message joined together. Any other
// text, including a request carried inside the instructions, makes it a
// prompt.
func IsCodexInjectedContext(text string) bool {
	rest := strings.TrimSpace(text)
	if rest == "" {
		return false
	}
	for rest != "" {
		matched := false
		for _, fragment := range codexContextFragments {
			if !strings.HasPrefix(rest, fragment.open) {
				continue
			}
			end := strings.Index(rest, fragment.close)
			if end < 0 || strings.Contains(rest[:end], codexRequestMarker) {
				return false
			}
			rest = strings.TrimSpace(rest[end+len(fragment.close):])
			matched = true
			break
		}
		if !matched {
			return false
		}
	}
	return true
}
