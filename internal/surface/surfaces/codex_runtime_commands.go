package surfaces

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// Codex runs a skill named with $; neither Codex route runs a message that
// starts with a slash, so nothing here inserts one.
func (c *Codex) Commands(ctx context.Context, sess *surface.Session) (*surface.RuntimeCommands, error) {
	if sess == nil || sess.Cwd == "" {
		return &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{}}, nil
	}
	resp, err := c.requestSession(ctx, sess, false, "skills/list", codexSkillsParams(sess), 5*time.Second)
	if err != nil {
		return nil, err
	}
	return codexRuntimeCommands(resp), nil
}

// Codex answers a folder it has listed from a cache that misses a skill added
// since, so every list rescans.
func codexSkillsParams(sess *surface.Session) map[string]any {
	return map[string]any{"cwds": []string{sess.Cwd}, "forceReload": true}
}

func codexRuntimeCommands(resp map[string]any) *surface.RuntimeCommands {
	out := &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{}}
	result, _ := resp["result"].(map[string]any)
	entries, _ := result["data"].([]any)
	seen := map[string]bool{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		skills, _ := entry["skills"].([]any)
		for _, rawSkill := range skills {
			skill, _ := rawSkill.(map[string]any)
			name := str(skill, "name")
			if name == "" || seen[name] || skill["enabled"] == false {
				continue
			}
			seen[name] = true
			out.Commands = append(out.Commands, surface.RuntimeCommand{Name: name, Description: codexSkillDescription(skill), Insert: "$" + name})
		}
	}
	return out
}

func codexSkillDescription(skill map[string]any) string {
	if face, _ := skill["interface"].(map[string]any); str(face, "shortDescription") != "" {
		return str(face, "shortDescription")
	}
	if short := str(skill, "shortDescription"); short != "" {
		return short
	}
	return str(skill, "description")
}

func (c *Codex) emitCommands(ctx context.Context, sess *surface.Session, onEvent func(surface.StreamEvent), version uint64) {
	commands, err := c.Commands(ctx, sess)
	if err != nil || commands == nil {
		return
	}
	onEvent(surface.StreamEvent{ID: "commands", ProviderKey: "commands", Version: version, Operation: "replace", Kind: "commands", Commands: commands})
}
