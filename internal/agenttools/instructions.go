package agenttools

// ServerInstructions is the agenthail MCP server's initialize instructions.
// Codex lists a deferred tool namespace by the first line of its server's
// instructions, cut at 250 characters, so that line alone must say what the
// tools are for.
const ServerInstructions = "Agenthail connects you to the person who runs you and to other agent sessions: put a question to the person (" + Ask + "), message a session (agenthail_send), and find and log project work (agenthail_ledger_read, agenthail_ledger_write).\n" +
	"agenthail_whoami says who you are, which project you are in and who you report to. When an agent started you, report to it with agenthail_send."
