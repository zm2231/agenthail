package surfaces

import (
	"github.com/zm2231/agenthail/internal/surface"
)

// SessionCapabilities narrows Capabilities for a session the host runs, which
// takes steer, interrupt and model changes but no Remote Control commands, and
// for any other session Control cannot reach through Remote Control: a CLI
// session reached only over its native peer socket, or any session on an
// install without Remote Control. Hosted and socket sessions stream only from
// a local transcript. A goal is set only where the session runs /goal as a
// command: its peer socket hands the text to the model as a message.
// Lifecycle follows the runtime: a background job and a host session have
// status, logs, stop and resume; an interactive session belongs to its
// terminal. A terminal session or background job can be adopted into the
// host; a host session already runs there.
func (c *Claude) SessionCapabilities(sess *surface.Session) surface.Capabilities {
	capabilities := c.Capabilities()
	capabilities.GoalSet = sess == nil || c.canSendHumanTurn(sess)
	if claudeHosted(sess) {
		capabilities.Stream = true
		capabilities.Compact = false
		capabilities.Commands = false
		capabilities.Respond = true
		capabilities.PermissionMode = true
		capabilities.Lifecycle = surface.Lifecycle{Status: true, Logs: true, Stop: true, Resume: true}
		return capabilities
	}
	if sess == nil {
		return capabilities
	}
	if sess.RuntimeKind != surface.RuntimeClaudeBackground {
		capabilities.Lifecycle = surface.Lifecycle{}
	}
	capabilities.Lifecycle.Adopt = claudeAdoptable(sess)
	if sess.Transport == "uds" {
		capabilities.Stream = sess.HasLocal && sess.Transcript != ""
	}
	capabilities.Respond = c.prompts.any(sess.ID)
	if c.localOnly || nativeClaudeOnly(sess) {
		capabilities.Steer = false
		capabilities.Compact = false
		capabilities.Model = false
		capabilities.Interrupt = false
		capabilities.Commands = false
	}
	if sess.Transport == "uds" && !c.canSendHumanTurn(sess) {
		capabilities.TurnNotice = c.turnNotice()
	}
	return capabilities
}

// turnNotice is for a live terminal session reached only over its peer
// socket, where a person's message is a note the model reads, not their turn.
// It names Remote Control only on an install that has it; a client that can
// adopt the session offers that remedy itself.
func (c *Claude) turnNotice() string {
	notice := "What you write reaches this terminal session as a note from Agenthail, not as your own turn."
	if c.localOnly {
		return notice
	}
	return notice + " Run /remote-control in its terminal to write as yourself."
}

// DisableRemoteControl limits the surface to local Claude Code sessions on an
// install that ships no sidecar, so health no longer probes claude.ai.
func (c *Claude) DisableRemoteControl() {
	c.localOnly = true
}

func (c *Claude) RemoteControlAvailable() bool { return !c.localOnly }
