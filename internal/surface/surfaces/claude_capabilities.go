package surfaces

import (
	"github.com/zm2231/agenthail/internal/surface"
)

// SessionCapabilities narrows Capabilities for a session the host runs, which
// takes steer, interrupt, compact and model changes but no Remote Control
// commands, and
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
	capabilities.RemoteControl = !c.localOnly
	capabilities.PersonMessage = c.Route(sess, surface.AuthorPerson).PersonMessage()
	if claudeHosted(sess) {
		capabilities.Stream = true
		capabilities.Commands = false
		capabilities.Respond = true
		capabilities.PermissionMode = true
		capabilities.SteerImages = true
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
	capabilities.Respond = c.answersPrompts(sess)
	if c.localOnly || nativeClaudeOnly(sess) {
		capabilities.Steer = false
		capabilities.Compact = false
		capabilities.Model = false
		capabilities.Interrupt = false
		capabilities.Commands = false
	}
	return capabilities
}

// DisableRemoteControl limits the surface to local Claude Code sessions on an
// install that ships no sidecar, so health no longer probes claude.ai.
func (c *Claude) DisableRemoteControl() {
	c.localOnly = true
}

func (c *Claude) RemoteControlAvailable() bool { return !c.localOnly }
