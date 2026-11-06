package agenttools

import "fmt"

type Line struct {
	Use  string
	Line string
}

type CardInput struct {
	ReportTo string
	Project  string
	MCP      bool
}

type cardLine struct {
	use  string
	cli  string
	mcp  string
	when func(CardInput) bool
	arg  func(CardInput) string
}

var cardLines = []cardLine{
	{
		use:  "report a result or a blocker",
		cli:  `agenthail send %s "<result>"`,
		mcp:  `agenthail_send {"target":"%s","message":"<result>"}`,
		when: func(in CardInput) bool { return in.ReportTo != "" },
		arg:  func(in CardInput) string { return in.ReportTo },
	},
	{use: "end every turn, after your reply", mcp: EndTurn + ` {"headline":"<one line>","needs":"you|nothing"}`},
	{use: "message another session", cli: `agenthail send <@alias|session-id> "<message>"`, mcp: `agenthail_send {"target":"<@alias|session-id>","message":"<message>"}`},
	{use: "find sessions and their aliases", cli: "agenthail list --json", mcp: "agenthail_sessions"},
	{use: "read a session's latest output", cli: "agenthail last <@alias|session-id>", mcp: `agenthail_read {"sessionId":"<@alias|session-id>"}`},
	{
		use:  "tell every session in this project",
		cli:  `agenthail channel send '#%s' "<message>"`,
		mcp:  `agenthail_channel_send {"channel":"#%s","message":"<message>"}`,
		when: func(in CardInput) bool { return in.Project != "" },
		arg:  func(in CardInput) string { return in.Project },
	},
	{use: "start a helper session", cli: `agenthail thread create <claude|codex> "<task>" --alias <name>`, mcp: `agenthail_create {"surface":"claude|codex","cwd":"<folder>","message":"<task>","alias":"<name>","task":true}`},
	{use: "wait for a helper's result", mcp: `agenthail_wait {"tasks":["<taskId>"]}`},
	{use: "check who you are now", cli: "agenthail whoami --json", mcp: "agenthail_whoami"},
}

func CardLines(in CardInput) []Line {
	var out []Line
	for _, line := range cardLines {
		if line.when != nil && !line.when(in) {
			continue
		}
		form := line.cli
		if in.MCP {
			form = line.mcp
		}
		if form == "" {
			continue
		}
		if line.arg != nil {
			form = fmt.Sprintf(form, line.arg(in))
		}
		out = append(out, Line{Use: line.use, Line: form})
	}
	return out
}

func Name(mcp bool, tool, cli string) string {
	if mcp {
		return tool
	}
	return cli
}
