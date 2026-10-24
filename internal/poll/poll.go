package poll

import (
	"context"
	"time"
)

func Until(ctx context.Context, every time.Duration, fn func() (bool, error)) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		done, err := fn()
		if done || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
