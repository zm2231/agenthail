package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type launchMCPCredentials struct {
	credential surface.LaunchMCPCredential
	claim      func(ref, threadID string) error
}

func (c launchMCPCredentials) URL() string { return c.credential.URL }

func (c launchMCPCredentials) Pending(context.Context) (string, string, error) {
	return c.credential.Token, c.credential.Ref, nil
}

func (c launchMCPCredentials) Claim(ref, threadID string) error { return c.claim(ref, threadID) }

func (c launchMCPCredentials) Live(string) (string, bool) { return "", false }

func (c launchMCPCredentials) Rotate(string) (string, error) {
	return "", errors.New("a terminal launch only starts its thread")
}

func (c launchMCPCredentials) Unattached(threadID string, err error) {
	fmt.Fprintf(os.Stderr, "agenthail: %s starts without the agenthail MCP server: %s\n", threadID, err)
}

func (a *App) managedCodexForLaunch(launchID string) (*surfaces.Codex, error) {
	codex := surfaces.NewCodex("")
	if launchID == "" {
		return codex, nil
	}
	credential, err := surface.TakeManagedCodexLaunchCredential(launchID)
	if err != nil || credential == nil {
		return codex, err
	}
	codex.SetMCPCredentials(launchMCPCredentials{credential: *credential, claim: a.claimMCPCredential})
	return codex, nil
}

func (a *App) claimMCPCredential(ref, threadID string) error {
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, status, err := client.call(ctx, http.MethodPost, daemon.MCPClaimPath, nil, map[string]string{"ref": ref, "threadId": threadID}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("daemon refused the claim (%d): %s", status, body)
	}
	return nil
}
