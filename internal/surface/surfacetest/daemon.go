package surfacetest

// InDaemon is c as the daemon runs it, the one process that starts, forks
// and loads conversations.
func InDaemon[T interface{ OpensConversations(func(string, error)) }](c T) T {
	c.OpensConversations(func(string, error) {})
	return c
}
