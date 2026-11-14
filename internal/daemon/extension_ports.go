package daemon

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	defaultExtensionPortBase       = 7420
	defaultRemoteExtensionPortBase = 7620
	extensionPortBaseEnv           = "AGENTHAIL_EXTENSION_PORT_BASE"
	remoteExtensionPortBaseEnv     = "AGENTHAIL_REMOTE_EXTENSION_PORT_BASE"
)

func (d *Daemon) configureExtensionPorts() {
	if os.Getenv(extensionPortBaseEnv) == "" && os.Getenv(remoteExtensionPortBaseEnv) == "" {
		return
	}
	dashboardPort := 0
	if config, err := LoadDashboardConfig(); err == nil {
		if _, port, err := net.SplitHostPort(config.Listen); err == nil {
			dashboardPort, _ = strconv.Atoi(port)
		}
	}
	local, remote, problems := extensionPortBases(os.Getenv, dashboardPort)
	for _, problem := range problems {
		d.log.Printf("extension ports: %s", problem)
	}
	extensionPortBase, remoteExtensionPortBase = local, remote
}

func extensionPortBases(getenv func(string) string, dashboardPort int) (int, int, []string) {
	var problems []string
	pick := func(name string, fallback, count int, taken ...[2]int) int {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return fallback
		}
		base, err := strconv.Atoi(value)
		problem := ""
		switch {
		case err != nil:
			problem = "is not a number"
		case base < 1024 || base+count > 65536:
			problem = fmt.Sprintf("needs %d ports from 1024 up to 65535", count)
		case dashboardPort >= base && dashboardPort < base+count:
			problem = fmt.Sprintf("would take the dashboard's port %d", dashboardPort)
		}
		for _, other := range taken {
			if problem == "" && base < other[1] && other[0] < base+count {
				problem = fmt.Sprintf("overlaps the other extension pool, %d to %d", other[0], other[1]-1)
			}
		}
		if problem != "" {
			problems = append(problems, fmt.Sprintf("%s=%s %s; using %d", name, value, problem, fallback))
			return fallback
		}
		return base
	}
	local := pick(extensionPortBaseEnv, defaultExtensionPortBase, extensionPortCount)
	remote := pick(remoteExtensionPortBaseEnv, defaultRemoteExtensionPortBase, remoteExtensionPortCount, [2]int{local, local + extensionPortCount})
	if remote == defaultRemoteExtensionPortBase && local < remote+remoteExtensionPortCount && remote < local+extensionPortCount {
		problems = append(problems, fmt.Sprintf("%s=%d overlaps the other extension pool; using %d", extensionPortBaseEnv, local, defaultExtensionPortBase))
		local = defaultExtensionPortBase
	}
	return local, remote, problems
}
