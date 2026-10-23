package daemon

import (
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/zm2231/agenthail/internal/tailscale"
)

const DefaultRemoteAccessPort = 7412

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

func normalizeRemoteAccessConfig(config RemoteAccessConfig) RemoteAccessConfig {
	if config.Provider == "" {
		config.Provider = "tailscale"
	}
	if config.Port == 0 {
		config.Port = DefaultRemoteAccessPort
	}
	return config
}

func RemoteAccessStatusForConfig(config DashboardConfig) RemoteAccessStatus {
	remote := normalizeRemoteAccessConfig(config.RemoteAccess)
	result := RemoteAccessStatus{Desired: remote.Enabled, Provider: remote.Provider, Port: remote.Port}
	path, err := tailscale.Executable(remote.TailscalePath)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	run := tailscale.Command(path)
	node, err := tailscale.ReadNode(run)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.DNSName = node.Self.DNSName
	serve, err := tailscale.ReadServe(run)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Proxy = serve.Proxy(node.Self.DNSName, remote.Port)
	httpsReady, err := tailscale.ServesHTTPS(run, node.Self.DNSName, remote.Port)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if serve.Funnel(node.Self.DNSName, remote.Port) {
		result.Error = "Tailscale Funnel is enabled on the Agenthail port"
		return result
	}
	target, _ := tailscale.Target(config.Listen)
	result.Enabled = serve.Routes(node.Self.DNSName, target, remote.Port, httpsReady)
	if result.Enabled {
		result.URL = tailscale.URL(node.Self.DNSName, remote.Port)
	}
	return result
}

func EnableRemoteAccess(config DashboardConfig) (DashboardConfig, RemoteAccessStatus, error) {
	config.RemoteAccess = normalizeRemoteAccessConfig(config.RemoteAccess)
	path, err := tailscale.Executable(config.RemoteAccess.TailscalePath)
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
	run := tailscale.Command(path)
	node, err := tailscale.ReadNode(run)
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
	target, err := tailscale.Target(config.Listen)
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
	if err := tailscale.Publish(run, node.Self.DNSName, target, config.RemoteAccess.Port); err != nil {
		return config, RemoteAccessStatus{}, err
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
	path, err := tailscale.Executable(config.RemoteAccess.TailscalePath)
	if err != nil {
		return config, err
	}
	run := tailscale.Command(path)
	node, err := tailscale.ReadNode(run)
	if err != nil {
		return config, err
	}
	target, err := tailscale.Target(config.Listen)
	if err != nil {
		return config, err
	}
	if _, err := tailscale.Unpublish(run, node.Self.DNSName, target, config.RemoteAccess.Port); err != nil {
		return config, err
	}
	config.RemoteAccess.Enabled = false
	return config, SaveDashboardConfig(config)
}

func RemoteAccessQR(value string) ([]byte, error) {
	return qrcode.Encode(value, qrcode.Medium, 512)
}
