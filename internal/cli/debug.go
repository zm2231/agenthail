package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

const debugUsage = "usage: agenthail debug heap <dir> | agenthail debug cpu <dir> [seconds]"

func (a *App) cmdDebug(args []string) error {
	if len(args) < 2 || strings.HasPrefix(args[1], "-") {
		return errors.New(debugUsage)
	}
	seconds := daemon.DefaultCPUProfileSeconds
	switch {
	case args[0] == "heap" && len(args) == 2:
	case args[0] == "cpu" && len(args) == 2:
	case args[0] == "cpu" && len(args) == 3:
		parsed, err := strconv.Atoi(args[2])
		if err != nil || parsed < 1 || parsed > daemon.MaxCPUProfileSeconds {
			return fmt.Errorf("seconds must be between 1 and %d", daemon.MaxCPUProfileSeconds)
		}
		seconds = parsed
	default:
		return errors.New(debugUsage)
	}
	if !a.daemonIsRunning() {
		return errors.New("the daemon is not running; start it with: agenthail daemon start")
	}
	dir := args[1]
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	socket := daemon.DebugSocketPath()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", socket)
	}}}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	if args[0] == "cpu" {
		path := filepath.Join(dir, "cpu-"+stamp+".pb.gz")
		fmt.Fprintf(os.Stderr, "sampling the daemon's CPU for %ds\n", seconds)
		if err := fetchDebugFile(client, fmt.Sprintf("profiles/cpu?seconds=%d", seconds), path, time.Duration(seconds)*time.Second+time.Minute); err != nil {
			return fmt.Errorf("cpu profile: %w", err)
		}
		fmt.Println(path)
		return nil
	}
	// Memory statistics come first: the heap profile collects garbage before
	// it is written.
	path := filepath.Join(dir, "memstats-"+stamp+".json")
	if err := fetchDebugFile(client, "memstats", path, 2*time.Minute); err != nil {
		return fmt.Errorf("memory statistics: %w", err)
	}
	fmt.Println(path)
	for _, name := range daemon.DebugProfiles {
		path := filepath.Join(dir, fmt.Sprintf("%s-%s.pb.gz", name, stamp))
		if err := fetchDebugFile(client, "profiles/"+name, path, 2*time.Minute); err != nil {
			return fmt.Errorf("%s profile: %w", name, err)
		}
		fmt.Println(path)
	}
	return nil
}

var createProfileFile = func(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

func fetchDebugFile(client *http.Client, endpoint, path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/"+endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("reach the daemon's debug socket: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("daemon answered %s: %s", response.Status, body)
	}
	file, err := createProfileFile(path)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, response.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}
