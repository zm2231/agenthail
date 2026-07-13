package daemon

import (
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

	qrcode "github.com/skip2/go-qrcode"
)

const defaultRemoteAccessPort = 7412

type RemoteAccessConfig struct {
	Enabled       bool   `json:"enabled"`
	Provider      string `json:"provider"`
	Port          int    `json:"port"`
	TailscalePath string `json:"tailscalePath,omitempty"`
}

type RemoteAccessStatus struct {
	Enabled  bool   `json:"enabled"`
	Desired  bool   `json:"desired"`
	Provider string `json:"provider"`
	URL      string `json:"url,omitempty"`
	DNSName  string `json:"dnsName,omitempty"`
	Port     int    `json:"port"`
	Proxy    string `json:"proxy,omitempty"`
	Error    string `json:"error,omitempty"`
}

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

func normalizeRemoteAccessConfig(config RemoteAccessConfig) RemoteAccessConfig {
	if config.Provider == "" {
		config.Provider = "tailscale"
	}
	if config.Port == 0 {
		config.Port = defaultRemoteAccessPort
	}
	return config
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
		if info, err := os.Stat(resolved); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("Tailscale is not installed")
}

func runTailscale(path string, args ...string) ([]byte, error) {
	command := exec.Command(path, args...)
	command.Env = append(os.Environ(), "SHELL=/bin/zsh", "TERM=xterm-256color")
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("tailscale %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func remoteNode(path string) (tailscaleNodeStatus, error) {
	output, err := runTailscale(path, "status", "--json")
	if err != nil {
		return tailscaleNodeStatus{}, err
	}
	var status tailscaleNodeStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return status, err
	}
	status.Self.DNSName = strings.TrimSuffix(status.Self.DNSName, ".")
	if status.Self.DNSName == "" {
		return status, fmt.Errorf("Tailscale MagicDNS is unavailable")
	}
	if !status.Self.Online && status.BackendState != "Running" {
		return status, fmt.Errorf("Tailscale is offline")
	}
	return status, nil
}

func remoteServe(path string) (tailscaleServeStatus, error) {
	output, err := runTailscale(path, "serve", "status", "--json")
	if err != nil {
		return tailscaleServeStatus{}, err
	}
	var status tailscaleServeStatus
	return status, json.Unmarshal(output, &status)
}

func remoteHost(dnsName string, port int) string {
	return net.JoinHostPort(dnsName, strconv.Itoa(port))
}

func remoteProxy(status tailscaleServeStatus, dnsName string, port int) string {
	return status.Web[remoteHost(dnsName, port)].Handlers["/"].Proxy
}

func remoteTarget(listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", fmt.Errorf("dashboard listen address must be host:port")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port), nil
}

func remoteURL(dnsName, localURL string, port int) (string, error) {
	local, err := url.Parse(localURL)
	if err != nil || local.Query().Get("token") == "" {
		return "", fmt.Errorf("dashboard access token is unavailable")
	}
	return (&url.URL{Scheme: "http", Host: remoteHost(dnsName, port), Path: "/", RawQuery: local.RawQuery, Fragment: "overview"}).String(), nil
}

func RemoteAccessStatusForConfig(config DashboardConfig) RemoteAccessStatus {
	remote := normalizeRemoteAccessConfig(config.RemoteAccess)
	result := RemoteAccessStatus{Desired: remote.Enabled, Provider: remote.Provider, Port: remote.Port}
	path, err := tailscaleExecutable(remote.TailscalePath)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	node, err := remoteNode(path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.DNSName = node.Self.DNSName
	serve, err := remoteServe(path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Proxy = remoteProxy(serve, node.Self.DNSName, remote.Port)
	target, _ := remoteTarget(config.Listen)
	if serve.AllowFunnel[remoteHost(node.Self.DNSName, remote.Port)] {
		result.Error = "Tailscale Funnel is enabled on the Agenthail port"
		return result
	}
	result.Enabled = result.Proxy == target
	if result.Enabled {
		if local, urlErr := DashboardURL(); urlErr == nil {
			result.URL, _ = remoteURL(node.Self.DNSName, local, remote.Port)
		}
	}
	return result
}

func EnableRemoteAccess(config DashboardConfig) (DashboardConfig, RemoteAccessStatus, error) {
	config.RemoteAccess = normalizeRemoteAccessConfig(config.RemoteAccess)
	path, err := tailscaleExecutable(config.RemoteAccess.TailscalePath)
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
	node, err := remoteNode(path)
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
	status, err := remoteServe(path)
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
	host := remoteHost(node.Self.DNSName, config.RemoteAccess.Port)
	if status.AllowFunnel[host] {
		return config, RemoteAccessStatus{}, fmt.Errorf("Tailscale Funnel is enabled on port %d; disable Funnel first", config.RemoteAccess.Port)
	}
	target, err := remoteTarget(config.Listen)
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
	proxy := remoteProxy(status, node.Self.DNSName, config.RemoteAccess.Port)
	if _, used := status.TCP[strconv.Itoa(config.RemoteAccess.Port)]; used && proxy != "" && proxy != target {
		return config, RemoteAccessStatus{}, fmt.Errorf("port %d already proxies another service", config.RemoteAccess.Port)
	}
	if _, used := status.TCP[strconv.Itoa(config.RemoteAccess.Port)]; used && proxy == "" {
		return config, RemoteAccessStatus{}, fmt.Errorf("port %d is already in use", config.RemoteAccess.Port)
	}
	if proxy != target {
		if _, err := runTailscale(path, "serve", "--bg", "--yes", "--http="+strconv.Itoa(config.RemoteAccess.Port), target); err != nil {
			return config, RemoteAccessStatus{}, err
		}
	}
	config.RemoteAccess.Enabled = true
	config.RemoteAccess.TailscalePath = path
	if err := SaveDashboardConfig(config); err != nil {
		return config, RemoteAccessStatus{}, err
	}
	result := RemoteAccessStatusForConfig(config)
	if !result.Enabled {
		return config, result, fmt.Errorf("remote access did not become ready: %s", result.Error)
	}
	return config, result, nil
}

func DisableRemoteAccess(config DashboardConfig) (DashboardConfig, error) {
	config.RemoteAccess = normalizeRemoteAccessConfig(config.RemoteAccess)
	path, err := tailscaleExecutable(config.RemoteAccess.TailscalePath)
	if err != nil {
		return config, err
	}
	node, err := remoteNode(path)
	if err != nil {
		return config, err
	}
	status, err := remoteServe(path)
	if err != nil {
		return config, err
	}
	target, err := remoteTarget(config.Listen)
	if err != nil {
		return config, err
	}
	proxy := remoteProxy(status, node.Self.DNSName, config.RemoteAccess.Port)
	if proxy != "" && proxy != target {
		return config, fmt.Errorf("refusing to remove a remote route owned by another service")
	}
	if proxy == target {
		if _, err := runTailscale(path, "serve", "--http="+strconv.Itoa(config.RemoteAccess.Port), "off"); err != nil {
			return config, err
		}
	}
	config.RemoteAccess.Enabled = false
	return config, SaveDashboardConfig(config)
}

func RemoteAccessQR(value string) ([]byte, error) {
	return qrcode.Encode(value, qrcode.Medium, 512)
}
