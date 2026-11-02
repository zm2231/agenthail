package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

func (a *App) ledgerG2(projectKey string) (daemon.G2Count, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return daemon.G2Count{}, err
	}
	data, status, err := client.call(context.Background(), http.MethodGet, "/api/v1/ledger/g2", url.Values{"projectKey": {projectKey}}, nil, nil)
	if err != nil {
		return daemon.G2Count{}, err
	}
	if status != http.StatusOK {
		return daemon.G2Count{}, actionError(data)
	}
	var count daemon.G2Count
	return count, json.Unmarshal(data, &count)
}

func g2Line(count daemon.G2Count, err error) string {
	if err != nil {
		return "G2: no turn-end checks are running (" + err.Error() + ")"
	}
	return fmt.Sprintf("G2: %d turns checked since %s, %d gated", count.Checked, count.Since.Local().Format(time.DateTime), count.Gated)
}
