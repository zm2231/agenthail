package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/zm2231/agenthail/internal/daemon"
)

const dashboardSharePort = 7412

type dashboardShareRunner func(args ...string) ([]byte, error)

type tailscaleNodeStatus struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		DNSName string `json:"DNSName"`
		Online  bool   `json:"Online"`
	} `json:"Self"`
}

type tailscaleServeStatus struct {
	AllowFunnel map[string]bool `json:"AllowFunnel"`
	TCP         map[string]struct {
		HTTP bool `json:"HTTP"`
	} `json:"TCP"`
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

func funnelEnabled(status tailscaleServeStatus, dnsName string, port int) bool {
	return status.AllowFunnel[net.JoinHostPort(dnsName, strconv.Itoa(port))]
}

type dashboardShareResult struct {
	URL     string `json:"url"`
	QRPath  string `json:"qrPath"`
	DNSName string `json:"dnsName"`
	Port    int    `json:"port"`
}

func dashboardShareQRPath() string {
	return filepath.Join(filepath.Dir(daemon.PidFilePath()), "dashboard-share.png")
}

func tailscaleExecutable(explicit string) (string, error) {
	candidates := []string{explicit, os.Getenv("AGENTHAIL_TAILSCALE")}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	if path, err := exec.LookPath("tailscale"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			resolved = candidate
		}
		info, err := os.Stat(resolved)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("Tailscale CLI was not found; install Tailscale, sign in, then run 'agenthail dashboard share' again")
}

func tailscaleRunner(path string) dashboardShareRunner {
	return func(args ...string) ([]byte, error) {
		command := exec.Command(path, args...)
		output, err := command.CombinedOutput()
		if err != nil {
			return output, fmt.Errorf("tailscale %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
		return output, nil
	}
}

func readTailscaleNode(run dashboardShareRunner) (tailscaleNodeStatus, error) {
	output, err := run("status", "--json")
	if err != nil {
		return tailscaleNodeStatus{}, err
	}
	var status tailscaleNodeStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return tailscaleNodeStatus{}, fmt.Errorf("parse Tailscale status: %w", err)
	}
	status.Self.DNSName = strings.TrimSuffix(status.Self.DNSName, ".")
	if status.Self.DNSName == "" {
		return tailscaleNodeStatus{}, fmt.Errorf("Tailscale did not report a MagicDNS name; enable MagicDNS for this tailnet")
	}
	if !status.Self.Online && status.BackendState != "Running" {
		return tailscaleNodeStatus{}, fmt.Errorf("Tailscale is installed but offline; connect it before sharing the dashboard")
	}
	return status, nil
}

func readTailscaleServe(run dashboardShareRunner) (tailscaleServeStatus, error) {
	output, err := run("serve", "status", "--json")
	if err != nil {
		return tailscaleServeStatus{}, err
	}
	var status tailscaleServeStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return tailscaleServeStatus{}, fmt.Errorf("parse Tailscale Serve status: %w", err)
	}
	return status, nil
}

func serveProxy(status tailscaleServeStatus, dnsName string, port int) string {
	host := net.JoinHostPort(dnsName, strconv.Itoa(port))
	if port == 80 {
		host = dnsName + ":80"
	}
	entry, ok := status.Web[host]
	if !ok {
		return ""
	}
	return entry.Handlers["/"].Proxy
}

func configureDashboardShare(run dashboardShareRunner, dnsName, target string, port int) error {
	status, err := readTailscaleServe(run)
	if err != nil {
		return err
	}
	portKey := strconv.Itoa(port)
	proxy := serveProxy(status, dnsName, port)
	if funnelEnabled(status, dnsName, port) {
		return fmt.Errorf("Tailscale Funnel is enabled on port %d; disable Funnel before sharing Agenthail privately", port)
	}
	if _, used := status.TCP[portKey]; used && proxy != "" && proxy != target {
		return fmt.Errorf("Tailscale Serve port %d already proxies %s; run 'agenthail dashboard share off' only if that route belongs to Agenthail", port, proxy)
	}
	if _, used := status.TCP[portKey]; used && proxy == "" {
		return fmt.Errorf("Tailscale Serve port %d is already in use by another service", port)
	}
	if proxy != target {
		if _, err := run("serve", "--bg", "--yes", "--http="+portKey, target); err != nil {
			return err
		}
	}
	status, err = readTailscaleServe(run)
	if err != nil {
		return err
	}
	if serveProxy(status, dnsName, port) != target {
		return fmt.Errorf("Tailscale Serve did not retain the Agenthail proxy on port %d", port)
	}
	if funnelEnabled(status, dnsName, port) {
		return fmt.Errorf("Tailscale Funnel became enabled on port %d; sharing was not accepted", port)
	}
	return nil
}

func remoteDashboardURL(dnsName, localURL string, port int) (string, error) {
	local, err := url.Parse(localURL)
	if err != nil {
		return "", fmt.Errorf("parse local dashboard URL: %w", err)
	}
	if local.Query().Get("token") == "" {
		return "", fmt.Errorf("local dashboard URL is missing its access token")
	}
	remote := &url.URL{Scheme: "http", Host: net.JoinHostPort(dnsName, strconv.Itoa(port)), Path: "/", RawQuery: local.RawQuery, Fragment: "conversations"}
	return remote.String(), nil
}

func dashboardShareTarget(listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", fmt.Errorf("dashboard listen address must be host:port")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port), nil
}

func writeDashboardQR(value, path string) error {
	data, err := qrcode.Encode(value, qrcode.Medium, 512)
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

func openDashboardShare(result dashboardShareResult) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if err := exec.Command("open", result.QRPath).Run(); err != nil {
		return fmt.Errorf("open dashboard QR: %w", err)
	}
	if err := exec.Command("open", result.URL).Run(); err != nil {
		return fmt.Errorf("open shared dashboard: %w", err)
	}
	return nil
}

func printDashboardShare(result dashboardShareResult, asJSON, copied bool) error {
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"url": result.URL, "qrPath": result.QRPath, "dnsName": result.DNSName, "port": result.Port, "copied": copied})
	}
	fmt.Println("dashboard shared privately through Tailscale")
	fmt.Printf("phone: %s\n", result.URL)
	fmt.Printf("QR: %s\n", result.QRPath)
	if copied {
		fmt.Println("copied the phone URL to the clipboard")
	}
	fmt.Println("on iPhone: open the URL, tap Share, then tap Add to Home Screen")
	fmt.Println("disable: agenthail dashboard share off")
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
	if !daemonServiceLoaded() {
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

func (a *App) cmdDashboardShare(args, positional []string) error {
	action := "on"
	if len(positional) > 0 {
		action = positional[0]
	}
	path, err := tailscaleExecutable(flagVal(args, "--tailscale"))
	if err != nil {
		return err
	}
	run := tailscaleRunner(path)
	node, err := readTailscaleNode(run)
	if err != nil {
		return err
	}
	asJSON := hasFlag(args, "--json")
	switch action {
	case "off":
		status, err := readTailscaleServe(run)
		if err != nil {
			return err
		}
		config, err := daemon.LoadDashboardConfig()
		if err != nil {
			return err
		}
		target, err := dashboardShareTarget(config.Listen)
		if err != nil {
			return err
		}
		proxy := serveProxy(status, node.Self.DNSName, dashboardSharePort)
		if proxy == "" {
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{"enabled": false, "port": dashboardSharePort})
			}
			fmt.Println("dashboard sharing is already disabled")
			return nil
		}
		if proxy != target {
			return fmt.Errorf("refusing to remove Tailscale Serve port %d because it now proxies %s", dashboardSharePort, proxy)
		}
		if _, err := run("serve", "--http="+strconv.Itoa(dashboardSharePort), "off"); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"enabled": false, "port": dashboardSharePort})
		}
		fmt.Println("dashboard sharing disabled")
		return nil
	case "status":
		status, err := readTailscaleServe(run)
		if err != nil {
			return err
		}
		config, err := daemon.LoadDashboardConfig()
		if err != nil {
			return err
		}
		target, err := dashboardShareTarget(config.Listen)
		if err != nil {
			return err
		}
		enabled := config.Enabled && serveProxy(status, node.Self.DNSName, dashboardSharePort) == target && target != "" && !funnelEnabled(status, node.Self.DNSName, dashboardSharePort)
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"enabled": enabled, "dashboardEnabled": config.Enabled, "dnsName": node.Self.DNSName, "port": dashboardSharePort, "proxy": serveProxy(status, node.Self.DNSName, dashboardSharePort)})
		}
		if !enabled {
			fmt.Println("dashboard share: off (run 'agenthail dashboard share')")
			return nil
		}
		localURL, err := daemon.DashboardURL()
		if err != nil {
			return err
		}
		remoteURL, err := remoteDashboardURL(node.Self.DNSName, localURL, dashboardSharePort)
		if err != nil {
			return err
		}
		fmt.Println("dashboard share: on")
		fmt.Printf("phone: %s\n", remoteURL)
		return nil
	case "on":
	default:
		return fmt.Errorf("usage: agenthail dashboard share [status|off] [--no-open] [--json] [--tailscale <path>]")
	}
	config, err := a.ensureDashboardReady()
	if err != nil {
		return err
	}
	target, err := dashboardShareTarget(config.Listen)
	if err != nil {
		return err
	}
	if err := configureDashboardShare(run, node.Self.DNSName, target, dashboardSharePort); err != nil {
		return err
	}
	localURL, err := daemon.DashboardURL()
	if err != nil {
		return err
	}
	remoteURL, err := remoteDashboardURL(node.Self.DNSName, localURL, dashboardSharePort)
	if err != nil {
		return err
	}
	result := dashboardShareResult{URL: remoteURL, QRPath: dashboardShareQRPath(), DNSName: node.Self.DNSName, Port: dashboardSharePort}
	if err := writeDashboardQR(result.URL, result.QRPath); err != nil {
		return err
	}
	copied := false
	if !asJSON {
		copied = copyDashboardURL(result.URL)
	}
	if err := printDashboardShare(result, asJSON, copied); err != nil {
		return err
	}
	if !hasFlag(args, "--no-open") && !asJSON {
		return openDashboardShare(result)
	}
	return nil
}
