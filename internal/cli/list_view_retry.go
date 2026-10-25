package cli

import (
	"context"
	"errors"
	"net/url"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/hosts"
)

const listViewAttempts = 3

var errListViewChanged = errors.New("catalog changed while listing; retry the command")

func readListViewConsistent(ctx context.Context, client *daemonAPIClient, query url.Values) (daemon.SessionViewSnapshot, error) {
	var err error
	for range listViewAttempts {
		var state daemon.SessionViewSnapshot
		state, err = readListView(ctx, client, cloneQuery(query))
		if err == nil || !listViewChanged(err) {
			return state, err
		}
	}
	return daemon.SessionViewSnapshot{}, err
}

func listViewChanged(err error) bool {
	var status hosts.StatusError
	return errors.Is(err, errListViewChanged) || errors.As(err, &status) && status.Code == "catalog_changed"
}

func cloneQuery(query url.Values) url.Values {
	out := make(url.Values, len(query))
	for key, values := range query {
		out[key] = append([]string(nil), values...)
	}
	return out
}
