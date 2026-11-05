package hosts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

const UnprovenCode = "host_unproven"

func Unproven(remote string) string {
	return fmt.Sprintf("Host %s doesn't know this host as a paired host. On %s run 'agenthail hosts invite', then here run 'agenthail hosts add <pairing-link>'", remote, remote)
}

type TokenDevice struct {
	Scopes []string `json:"scopes"`
	HostID string   `json:"hostId"`
}

// TokenDevice is what this daemon's token is on the host; false when the
// host is too old to say.
func (c *Client) TokenDevice(ctx context.Context) (TokenDevice, bool, error) {
	var self struct {
		Device TokenDevice `json:"device"`
	}
	err := c.GetJSON(ctx, "/api/v1/device", nil, &self)
	var status StatusError
	if errors.As(err, &status) && (status.Status == http.StatusMethodNotAllowed || status.Code == "unknown_route") {
		return TokenDevice{}, false, nil
	}
	return self.Device, err == nil, err
}
