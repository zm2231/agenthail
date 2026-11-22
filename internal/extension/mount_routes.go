package extension

import (
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/tailscale"
)

func MountTarget(port int) string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// WithdrawMountRoutes removes every mount route Agenthail published, for
// turning remote access off while no daemon runs to do it.
func WithdrawMountRoutes(home string, run tailscale.Runner, dnsName string) error {
	ports, err := ReadMountPorts(home)
	if err != nil {
		return fmt.Errorf("read mount ports: %w", err)
	}
	var failed []string
	for _, key := range slices.Sorted(maps.Keys(ports)) {
		assigned := ports[key]
		if !assigned.Published {
			continue
		}
		if _, err := tailscale.Unpublish(run, dnsName, MountTarget(assigned.Port), assigned.Port); err != nil {
			failed = append(failed, fmt.Sprintf("%s on port %d: %s", key, assigned.Port, err))
			continue
		}
		assigned.Published = false
		ports[key] = assigned
	}
	if err := WriteMountPorts(home, ports); err != nil {
		return fmt.Errorf("record mount ports: %w", err)
	}
	if len(failed) > 0 {
		return fmt.Errorf("these extension mounts are still on the tailnet: %s", strings.Join(failed, "; "))
	}
	return nil
}
