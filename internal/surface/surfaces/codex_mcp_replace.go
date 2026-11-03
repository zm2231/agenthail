package surfaces

import "context"

// Codex keeps a loaded thread's first config, so a rotated token reaches the thread when it next takes input or resumes.
func (c *Codex) ReplaceMCPCredential(_ context.Context, sessionID, _, _ string) error {
	c.markStaleMCP(sessionID)
	return nil
}
