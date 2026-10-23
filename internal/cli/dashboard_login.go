package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

type dashboardLoginLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (a *App) createDashboardLoginLink(remote bool) (dashboardLoginLink, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return dashboardLoginLink{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	data, status, err := client.call(ctx, http.MethodPost, "/api/v1/dashboard/login-links", nil, map[string]bool{"remote": remote}, nil)
	if err != nil {
		return dashboardLoginLink{}, err
	}
	if status != http.StatusOK {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error.Message != "" {
			return dashboardLoginLink{}, errors.New(failure.Error.Message)
		}
		return dashboardLoginLink{}, fmt.Errorf("create a dashboard login link: HTTP %d", status)
	}
	var link dashboardLoginLink
	if err := json.Unmarshal(data, &link); err != nil {
		return dashboardLoginLink{}, fmt.Errorf("read the dashboard login link: %w", err)
	}
	return link, nil
}

func (link dashboardLoginLink) validity() string {
	return fmt.Sprintf("works once, until %s", link.ExpiresAt.Local().Format(time.Kitchen))
}

func (a *App) cmdDashboardToken(positional []string) error {
	if len(positional) != 1 || positional[0] != "rotate" {
		return fmt.Errorf("usage: agenthail dashboard token rotate")
	}
	if err := daemon.RotateDashboardToken(); err != nil {
		return err
	}
	if _, running := daemon.IsRunning(); !running {
		fmt.Println("dashboard token rotated")
		return nil
	}
	if err := a.restartDaemonForDashboard(); err != nil {
		return fmt.Errorf("dashboard token rotated, but the daemon did not restart to use it: %w", err)
	}
	fmt.Println("dashboard token rotated; the daemon restarted to use it")
	return nil
}
