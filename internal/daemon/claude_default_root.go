package daemon

type ClaudeSettings struct {
	DefaultRoot string `json:"defaultRoot,omitempty"`
}

func (c DashboardConfig) ClaudeDefaultRoot() string {
	if c.Claude == nil {
		return ""
	}
	return c.Claude.DefaultRoot
}

func (c *DashboardConfig) SetClaudeDefaultRoot(name string) {
	if name == "" {
		c.Claude = nil
		return
	}
	c.Claude = &ClaudeSettings{DefaultRoot: name}
}
