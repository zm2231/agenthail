package cli

import (
	"context"
	"fmt"
	"os"
	"time"
)

// The registry is bound when it opens, so a command that never touches it does not open it.
func (a *App) writesWithin(ctx context.Context) (func(), error) {
	a.registryMu.Lock()
	defer a.registryMu.Unlock()
	if a.writeDeadline != nil {
		return func() {}, nil
	}
	a.writeDeadline = ctx
	if a.Registry != nil {
		if err := a.bindWritesLocked(); err != nil {
			a.writeDeadline = nil
			return nil, err
		}
	}
	return func() {
		a.registryMu.Lock()
		defer a.registryMu.Unlock()
		if a.releaseWrites != nil {
			a.releaseWrites()
		}
		a.writeDeadline, a.releaseWrites = nil, nil
	}, nil
}

func (a *App) bindWritesLocked() error {
	if a.writeDeadline == nil || a.Registry == nil || a.releaseWrites != nil {
		return nil
	}
	release, err := a.Registry.WriteWithin(a.writeDeadline)
	if err != nil {
		return fmt.Errorf("bound registry writes: %w", err)
	}
	a.releaseWrites = release
	return nil
}

func (a *App) deadlineContext(timeout time.Duration) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	release, err := a.writesWithin(ctx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return ctx, func() { release(); cancel() }, nil
}

const cleanupBudget = 2 * time.Second

// cleanUp runs a write that must happen even past the command's deadline, bounded by its own short budget.
func (a *App) cleanUp(what string, write func() error) {
	a.registryMu.Lock()
	outer := a.writeDeadline
	if a.releaseWrites != nil {
		a.releaseWrites()
	}
	parent := context.Background()
	if outer != nil {
		parent = context.WithoutCancel(outer)
	}
	ctx, cancel := context.WithTimeout(parent, cleanupBudget)
	defer cancel()
	a.writeDeadline, a.releaseWrites = ctx, nil
	err := a.bindWritesLocked()
	a.registryMu.Unlock()
	if err == nil {
		err = write()
	}
	a.registryMu.Lock()
	if a.releaseWrites != nil {
		a.releaseWrites()
	}
	a.writeDeadline, a.releaseWrites = outer, nil
	if rebind := a.bindWritesLocked(); err == nil {
		err = rebind
	}
	a.registryMu.Unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agenthail: %s: %v\n", what, err)
	}
}

const controlCommandTimeout = 30 * time.Second

func (a *App) timedContext(args []string, fallback time.Duration) (context.Context, context.CancelFunc, error) {
	timeout, err := commandTimeout(args, fallback)
	if err != nil {
		return nil, nil, err
	}
	return a.deadlineContext(timeout)
}
