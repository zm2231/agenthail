package agenttools

type Tool struct {
	Name        string
	Description string
	// Every byte of an always-loaded tool is in every turn's context.
	AlwaysLoad bool
}

const (
	EndTurn      = "agenthail_end_turn"
	Ask          = "agenthail_ask"
	EndTurnNudge = "[agenthail] This turn has no end yet. Call " + EndTurn + " now with a one-line headline. Don't repeat your reply."
)

var Tools = []Tool{
	{Name: "agenthail_whoami", Description: "Your session, alias, project, host, coordinator role, capabilities, ceiling and credential expiry."},
	{Name: "agenthail_sessions", Description: "Sessions you may read: your own, the ones you spawned, and your project's. Paged; pass nextCursor back as cursor. A session that appears while you page shows up when you list again from the start."},
	{Name: "agenthail_capabilities", Description: "What you may do: capabilities, ceiling, which sessions you can read, which surfaces you may start, and the output limits."},
	{Name: "agenthail_requests", Description: "The sessions you can read that are waiting on a prompt or question, or one session's open requests, in one call (prompts clipped to fit; pass requestId for one request's longer prompt). If truncated is true, total says how many there are."},
	{Name: "agenthail_ledger_read", Description: "Your project's intent ledger: the board, or one list (goals, asks, decisions, questions, work, ready, backlog, triage), paged."},
	{Name: "agenthail_read", Description: "A session's latest items, older pages through before, newer items through since, or a clipped item's full text through bodyRef. Results are capped at 16 KiB."},
	{Name: "agenthail_projects", Description: "The projects on this host and its paired hosts, with their coordinators. A project marked conflict has coordinators on two hosts and cannot be reached until one is chosen."},
	{Name: "agenthail_channel_send", Description: "Post a message to a channel or project, delivered to its members as from you."},
	{Name: "agenthail_coordinator", Description: "Set or clear a project's coordinator. For the person's voice operator only."},
	{Name: "agenthail_archive", Description: "Archive, or restore, a session you spawned or, as coordinator, lead."},
	{Name: "agenthail_history", Description: "Delivery history, or the queue of instructions waiting to be delivered, newest first and paged. For the person's voice operator only."},
	{Name: "agenthail_ledger_write", Description: "Record in your project's intent ledger: attach, clause, question or work, with the fields the CLI's ledger commands take. Recorded as your session."},
	{Name: "agenthail_create", AlwaysLoad: true, Description: "Start a Claude or Codex session with a first message. It is bounded by your own ceiling: no higher permission mode and no folder outside yours. Sessions nest at most 3 deep, with at most 8 running at once per spawner."},
	{Name: EndTurn, AlwaysLoad: true, Description: "Call this last in every turn, after your reply. headline is the line shown in the person's list and notification. A refused call does not end the turn; fix it and call again."},
	{Name: Ask, AlwaysLoad: true, Description: "Put a decision to the person, or to your lead or spawner when an agent started you: they see it in Needs you and answer from any device, and the answer arrives as your next message. Raise it before you end your turn; don't also ask in your reply."},
	{Name: "agenthail_report", Description: "If your spawner started you as a task, tell it you are done or failed, and the result it should read. The first report settles the task; you can keep working."},
	{Name: "agenthail_result", AlwaysLoad: true, Description: "A task's state and result, by taskId or child target, for its parent or anyone who can read the child. Long text is clipped to fit."},
	{Name: "agenthail_wait", AlwaysLoad: true, Description: "Wait until a task settles, its child asks for something, or its goal stops, and say which. Default 600 seconds, at most 3600; a Claude session Agenthail doesn't host waits at most 45, so call again with the cursor. If it times out, the tasks you started are still running, and you get one notice when each settles, so you can end your turn instead of waiting again."},
	{Name: "agenthail_send", AlwaysLoad: true, Description: "Send a message to a session (target) or to a project's coordinator (project). Queued by default; sessions you spawned or lead can also take mode now. A queued message may go to any session in your project, up to your spawner, or by project to any project's coordinator, attributed to you."},
	{Name: "agenthail_pr", AlwaysLoad: true, Description: "Link, unlink, watch or unwatch a session's pull request. gh pr create links its own."},
	{Name: "agenthail_steer", Description: "Steer the running turn of a session you spawned or, as coordinator, lead."},
	{Name: "agenthail_interrupt", Description: "Interrupt the running turn of a session you spawned or, as coordinator, lead."},
	{Name: "agenthail_goal", Description: "Set, edit, clear, pause or resume the goal of a Codex session you spawned or, as coordinator, lead, or change its token budget."},
	{Name: "agenthail_answer", Description: "Answer an open request routed to you, by its id: allow or deny a permission prompt, question or MCP input; give an agent's ask answers or a message; or decision escalate to pass it up. Recorded as answered by you."},
	{Name: "agenthail_session_settings", Description: "Read a session's effective settings, or change the model, permission mode or effort of a session you spawned or, as coordinator, lead, or compact its context. A permission mode cannot exceed the session's ceiling."},
	{Name: "agenthail_alias", Description: "Name a session you spawned or, as coordinator, lead, so it can be reached as @name."},
	{Name: "agenthail_queue", Description: "Retry or cancel a queued instruction, or clear a session's pending ones. For the person's voice operator only."},
	{Name: "agenthail_relay", Description: "Add, remove or list relays that pass one session's replies to another. For the person's voice operator only."},
	{Name: "agenthail_channel", Description: "Create, delete, rename or list channels, and add or remove their members. For the person's voice operator only."},
	{Name: "agenthail_simulator", Description: "Drive an iOS simulator on a Mac: acquire the iPhone or iPad, install a .app from your outDir, launch it, describe the screen, tap, type, swipe, screenshot, push, set a location, read logs, then release it. Only for iOS app work. acquire says how."},
	{Name: "agenthail_browser", Description: "A headless browser on your host with your own cookies: open a tab, snapshot its accessibility tree with element refs, read its text and links, scroll, dismiss overlays, screenshot it, and act, wait, answer dialogs, resize, list or close. Use it only when the request asks for browser work or names a page, never to check your own edit unasked. servers lists this host's listening ports and the session that started each; localhost reaches them. open says how to drive it."},
}

func Lookup(name string) (Tool, bool) {
	for _, tool := range Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}
