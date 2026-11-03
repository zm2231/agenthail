package daemon

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func dashboardStartCwd(value string) (string, error) {
	value = strings.TrimSpace(value)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	if value == "" || value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	value, err = filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(value)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory is not a directory")
	}
	return filepath.Clean(value), nil
}

func (d *Daemon) reserveStartName(w http.ResponseWriter, name string) (registry.AliasReservation, bool) {
	reservation, err := d.reserveName(name)
	if err != nil {
		writeActionResult(w, actionResult{}, err)
		return registry.AliasReservation{}, false
	}
	return reservation, true
}

func (d *Daemon) reserveName(name string) (registry.AliasReservation, error) {
	reservation, err := d.Registry.ReserveAlias(name, time.Now().Add(surfaceOperationTimeout+startNameMargin))
	if err == nil {
		return reservation, nil
	}
	if registry.AliasRefused(err) {
		return registry.AliasReservation{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return registry.AliasReservation{}, actionFailed(http.StatusInternalServerError, "internal_error", fmt.Sprintf("reserve conversation name: %s", err))
}
