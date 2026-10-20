package daemon

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const claudeAccountsTTL = 5 * time.Second

// claudeAccounts is read once per few seconds rather than per projected
// row: a catalog pass projects every session, and each read re-parses the
// Agenthail config.
func (d *Daemon) claudeAccounts() []surface.ClaudeAccount {
	d.claudeAccountsMu.Lock()
	defer d.claudeAccountsMu.Unlock()
	if d.claudeAccountList != nil && time.Since(d.claudeAccountsAt) < claudeAccountsTTL {
		return d.claudeAccountList
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	accounts, err := surface.ClaudeAccounts(home)
	if err != nil {
		return d.claudeAccountList
	}
	d.claudeAccountList, d.claudeAccountsAt = accounts, time.Now()
	return accounts
}

// pruneClaudeMoves removes the source copies moves retired more than
// surfaces.ClaudeMoveRetention ago.
func (d *Daemon) pruneClaudeMoves(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if home, err := os.UserHomeDir(); err == nil {
			if pruned, err := surfaces.PruneClaudeMoves(home, surfaces.ClaudeMoveRetention, time.Now()); err != nil {
				d.log.Printf("prune moved Claude sessions: %s", err)
			} else if len(pruned) > 0 {
				d.log.Printf("removed the recovery copies of %d moved Claude sessions", len(pruned))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// dashboardUsageLimit tells a client that the session's Claude account is out
// of usage, and which other accounts on this host it could move to.
type dashboardUsageLimit struct {
	Message      string                  `json:"message"`
	ResetsAt     string                  `json:"resetsAt,omitempty"`
	Account      *surface.SessionAccount `json:"account,omitempty"`
	Alternatives []surface.ClaudeAccount `json:"alternatives"`
}

// usageLimitOffer lists the signed-in accounts the session is not in, that
// do not share its account, and whose cached usage is not exhausted.
func (d *Daemon) usageLimitOffer(session *surface.Session, items []surface.TimelineItem, now time.Time) *dashboardUsageLimit {
	if session.Surface != surface.KindClaude {
		return nil
	}
	notice := surface.ActiveUsageLimit(items, now)
	if notice == nil {
		return nil
	}
	accounts := d.claudeAccounts()
	offer := &dashboardUsageLimit{Message: notice.Text, ResetsAt: notice.ResetsAt, Account: surface.ClaudeSessionAccount(accounts, session.Transcript), Alternatives: []surface.ClaudeAccount{}}
	var current *surface.ClaudeAccount
	if offer.Account != nil {
		if account, ok := surface.ClaudeAccountForRoot(accounts, offer.Account.Root); ok {
			current = &account
		}
	}
	for _, account := range accounts {
		if !account.SignedIn || current != nil && (account.Root == current.Root || slices.Contains(current.SameAccountAs, account.Root)) {
			continue
		}
		if _, exhausted := account.Usage.Exhausted(now); exhausted {
			continue
		}
		offer.Alternatives = append(offer.Alternatives, account)
	}
	return offer
}
