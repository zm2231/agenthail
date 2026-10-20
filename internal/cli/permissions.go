package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/permissions"
)

// cmdPermissions shows the daemon's macOS privacy grants, or has macOS ask
// for the ones it lacks. The daemon reads them: macOS answers for the
// process responsible for the one asking, which from a terminal is the
// terminal.
func (a *App) cmdPermissions(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	switch positional := stripFlags(args); {
	case len(positional) == 0:
		report, err := a.daemonPermissions(ctx)
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return json.NewEncoder(os.Stdout).Encode(report)
		}
		printPermissions(report)
		return nil
	case len(positional) == 1 && positional[0] == "request":
		client, err := a.permissionsClient()
		if err != nil {
			return err
		}
		if _, _, err := client.call(ctx, http.MethodPost, "/api/v1/permissions/request", nil, nil, nil); err != nil {
			return err
		}
		fmt.Println("macOS is asking on this Mac for each grant Agenthail Daemon lacks; run 'agenthail permissions' after answering")
		return nil
	}
	return fmt.Errorf("usage: agenthail permissions [request] [--json]")
}

func (a *App) permissionsClient() (*daemonAPIClient, error) {
	if !a.daemonIsRunning() {
		return nil, errors.New("the daemon is not running, and only it can read its own grants; start it with 'agenthail daemon start'")
	}
	return a.daemonAPI()
}

func (a *App) daemonPermissions(ctx context.Context) (permissions.Report, error) {
	var report permissions.Report
	client, err := a.permissionsClient()
	if err != nil {
		return report, err
	}
	err = client.getJSON(ctx, "/api/v1/permissions", nil, &report)
	return report, err
}

func printPermissions(report permissions.Report) {
	if report.Holder != "" {
		fmt.Printf("[permissions] held by %s\n", report.Holder)
	} else {
		fmt.Println("[permissions]")
	}
	if report.Problem != "" {
		fmt.Printf("  problem: %s\n", report.Problem)
	}
	for _, grant := range report.Grants {
		fmt.Printf("  %s\n", grant.Describe())
	}
}
