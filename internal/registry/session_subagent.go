package registry

import "github.com/zm2231/agenthail/internal/surface"

const sessionSubagentSelect = `s.parent_session_id,s.root_session_id,s.subagent_depth,s.agent_nickname,s.agent_role`

type subagentColumns struct {
	parent, root, nickname, role string
	depth                        int
}

func (c *subagentColumns) targets() []any {
	return []any{&c.parent, &c.root, &c.depth, &c.nickname, &c.role}
}

func (c subagentColumns) apply(session *surface.Session) {
	if c.parent == "" {
		session.Subagent = nil
		return
	}
	session.Subagent = &surface.Subagent{ParentID: c.parent, RootID: c.root, Depth: c.depth, Nickname: c.nickname, Role: c.role}
}

func subagentValues(session surface.Session) (parent, root string, depth int, nickname, role string) {
	if session.Subagent == nil {
		return "", "", 0, "", ""
	}
	return session.Subagent.ParentID, session.Subagent.RootID, session.Subagent.Depth, session.Subagent.Nickname, session.Subagent.Role
}
