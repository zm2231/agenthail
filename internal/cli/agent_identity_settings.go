package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/daemonapi"
)

func (a *App) setIdentitySettings(name, sendMode string, jsonOut bool) error {
	client, err := a.localDaemonAPI()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rolesTimeout)
	defer cancel()
	body, _, err := client.call(ctx, http.MethodPost, daemonapi.IdentitySettingsPath, nil, daemonapi.IdentitySettingsRequest{Identity: name, SendMode: sendMode}, nil)
	if err != nil {
		return err
	}
	var result daemonapi.IdentitySettingsResult
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	if jsonOut {
		return emit(result)
	}
	fmt.Printf("@%s's sends with no mode now go %s\n", result.Identity, result.SendMode)
	return nil
}
