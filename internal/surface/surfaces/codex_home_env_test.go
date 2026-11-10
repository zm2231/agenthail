package surfaces

import "context"

func dialManagedCodex(ctx context.Context) (*managedCodexClient, error) {
	return CodexHomeFromEnv().dialManaged(ctx)
}

func managedCodexSocketPath() string { return CodexHomeFromEnv().socketPath() }

func codexThreadLockPath(threadID string) string {
	return CodexHomeFromEnv().threadLockPath(threadID)
}

func holdCodexThread(ctx context.Context, threadID string) (func(), error) {
	return CodexHomeFromEnv().holdThread(ctx, threadID)
}
