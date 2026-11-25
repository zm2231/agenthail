package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

const codexRolloutCacheTrimEvery = time.Hour

func trimCodexRolloutCache(ctx context.Context) {
	ticker := time.NewTicker(codexRolloutCacheTrimEvery)
	defer ticker.Stop()
	for {
		codexsurface.TrimRolloutCache()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
