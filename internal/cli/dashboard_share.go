package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/tailscale"
)

type dashboardShareResult struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
	QRPath    string    `json:"qrPath"`
	DNSName   string    `json:"dnsName"`
	Port      int       `json:"port"`
}

func dashboardShareQRPath() string {
	return filepath.Join(filepath.Dir(daemon.PidFilePath()), "dashboard-share.png")
}

func writeDashboardQR(value, path string) error {
	data, err := daemon.RemoteAccessQR(value)
	if err != nil {
		return fmt.Errorf("encode dashboard QR: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create dashboard data directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write dashboard QR: %w", err)
	}
	return nil
}

func copyDashboardURL(value string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	command := exec.Command("pbcopy")
	command.Stdin = bytes.NewBufferString(value)
	return command.Run() == nil
}

// openDashboardShare shows the QR code only: opening the login link here would
// spend it on this computer's browser.
func openDashboardShare(result dashboardShareResult) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if err := exec.Command("open", result.QRPath).Run(); err != nil {
		return fmt.Errorf("open dashboard QR: %w", err)
	}
	return nil
}

func printDashboardRemote(result dashboardShareResult, asJSON, copied bool) error {
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"url": result.URL, "expiresAt": result.ExpiresAt, "qrPath": result.QRPath, "dnsName": result.DNSName, "port": result.Port, "copied": copied})
	}
	fmt.Println("remote dashboard access enabled through Tailscale")
	fmt.Printf("phone sign-in link (%s): %s\n", dashboardLoginLink{ExpiresAt: result.ExpiresAt}.validity(), result.URL)
	fmt.Printf("QR: %s\n", result.QRPath)
	if copied {
		fmt.Println("copied the phone link to the clipboard")
	}
	fmt.Println("on iPhone: open the link, tap Share, then tap Add to Home Screen")
	fmt.Println("disable: agenthail dashboard remote off")
	return nil
}

func (a *App) ensureDashboardReady() (daemon.DashboardConfig, error) {
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return daemon.DashboardConfig{}, err
	}
	changed := !config.Enabled
	if changed {
		config.Enabled = true
		if err := daemon.SaveDashboardConfig(config); err != nil {
			return daemon.DashboardConfig{}, err
		}
	}
	if !a.isDaemonServiceLoaded() {
		if err := a.daemonInstallService(); err != nil {
			return daemon.DashboardConfig{}, err
		}
	} else if changed || !dashboardDaemonRunning() {
		if err := a.restartDaemonForDashboard(); err != nil {
			return daemon.DashboardConfig{}, err
		}
	}
	if err := waitForDashboard(config.Listen, 15*time.Second); err != nil {
		return daemon.DashboardConfig{}, err
	}
	return config, nil
}

func dashboardDaemonRunning() bool {
	_, running := daemon.IsRunning()
	return running
}

func dashboardRemoteEnabled(config daemon.DashboardConfig, serve tailscale.Serve, dnsName, target string, httpsReady bool) bool {
	return config.Enabled && serve.Routes(dnsName, target, daemon.DefaultRemoteAccessPort, httpsReady)
}

func (a *App) cmdDashboardRemote(args, positional []string) error {
	action := "on"
	if len(positional) > 0 {
		action = positional[0]
	}
	path, err := tailscale.Executable(flagVal(args, "--tailscale"))
	if errors.Is(err, tailscale.ErrNotInstalled) {
		return fmt.Errorf("%w; install Tailscale, sign in, then run 'agenthail dashboard remote' again", err)
	}
	if err != nil {
		return err
	}
	run := tailscale.Command(path)
	node, err := tailscale.ReadNode(run)
	if err != nil {
		return err
	}
	asJSON := hasFlag(args, "--json")
	switch action {
	case "off":
		config, err := daemon.LoadDashboardConfig()
		if err != nil {
			return err
		}
		target, err := tailscale.Target(config.Listen)
		if err != nil {
			return err
		}
		removed, err := tailscale.Unpublish(run, node.Self.DNSName, target, daemon.DefaultRemoteAccessPort)
		if err != nil {
			return err
		}
		config.RemoteAccess.Enabled = false
		if err := daemon.SaveDashboardConfig(config); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"enabled": false, "port": daemon.DefaultRemoteAccessPort})
		}
		if removed {
			fmt.Println("remote dashboard access disabled")
		} else {
			fmt.Println("remote dashboard access is already disabled")
		}
		return nil
	case "status":
		status, err := tailscale.ReadServe(run)
		if err != nil {
			return err
		}
		config, err := daemon.LoadDashboardConfig()
		if err != nil {
			return err
		}
		target, err := tailscale.Target(config.Listen)
		if err != nil {
			return err
		}
		httpsReady, err := tailscale.ServesHTTPS(run, node.Self.DNSName, daemon.DefaultRemoteAccessPort)
		if err != nil {
			return err
		}
		enabled := dashboardRemoteEnabled(config, status, node.Self.DNSName, target, httpsReady)
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"enabled": enabled, "dashboardEnabled": config.Enabled, "dnsName": node.Self.DNSName, "https": httpsReady, "port": daemon.DefaultRemoteAccessPort, "proxy": status.Proxy(node.Self.DNSName, daemon.DefaultRemoteAccessPort)})
		}
		if !enabled {
			fmt.Println("remote dashboard access: off")
			return nil
		}
		fmt.Println("remote dashboard access: on")
		fmt.Printf("phone: %s (sign in with a link from 'agenthail dashboard remote')\n", tailscale.URL(node.Self.DNSName, daemon.DefaultRemoteAccessPort))
		return nil
	case "on":
	default:
		return fmt.Errorf("usage: agenthail dashboard remote [status|off] [--no-open] [--json] [--tailscale <path>]")
	}
	config, err := a.ensureDashboardReady()
	if err != nil {
		return err
	}
	target, err := tailscale.Target(config.Listen)
	if err != nil {
		return err
	}
	if err := tailscale.Publish(run, node.Self.DNSName, target, daemon.DefaultRemoteAccessPort); err != nil {
		return err
	}
	config.RemoteAccess = daemon.RemoteAccessConfig{Enabled: true, Provider: "tailscale", Port: daemon.DefaultRemoteAccessPort, TailscalePath: path}
	if err := daemon.SaveDashboardConfig(config); err != nil {
		return err
	}
	link, err := a.createDashboardLoginLink(true)
	if err != nil {
		return err
	}
	result := dashboardShareResult{URL: link.URL, ExpiresAt: link.ExpiresAt, QRPath: dashboardShareQRPath(), DNSName: node.Self.DNSName, Port: daemon.DefaultRemoteAccessPort}
	if err := writeDashboardQR(result.URL, result.QRPath); err != nil {
		return err
	}
	copied := false
	if !asJSON {
		copied = copyDashboardURL(result.URL)
	}
	if err := printDashboardRemote(result, asJSON, copied); err != nil {
		return err
	}
	if !hasFlag(args, "--no-open") && !asJSON {
		return openDashboardShare(result)
	}
	return nil
}
