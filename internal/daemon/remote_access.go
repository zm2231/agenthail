package daemon

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/tailscale"
)

var remoteTailscale = func(explicit string) (tailscale.Runner, string, error) {
	path, err := tailscale.Executable(explicit)
	if err != nil {
		return nil, "", err
	}
	return tailscale.Command(path), path, nil
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

func RemoteAccessStatusForConfig(config dashconfig.Config) RemoteAccessStatus {
	remote := dashconfig.NormalizeRemoteAccess(config.RemoteAccess)
	result := RemoteAccessStatus{Desired: remote.Enabled, Provider: remote.Provider, Port: remote.Port}
	run, _, err := remoteTailscale(remote.TailscalePath)
	if err != nil {
		result.Error = err.Error()
		return result
	}
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

func EnableRemoteAccess(config dashconfig.Config) (dashconfig.Config, RemoteAccessStatus, error) {
	config.RemoteAccess = dashconfig.NormalizeRemoteAccess(config.RemoteAccess)
	run, path, err := remoteTailscale(config.RemoteAccess.TailscalePath)
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
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
	config, err = dashconfig.Update(func(saved *dashconfig.Config) error {
		saved.RemoteAccess.Enabled = true
		saved.RemoteAccess.TailscalePath = path
		return nil
	})
	if err != nil {
		return config, RemoteAccessStatus{}, err
	}
	result := RemoteAccessStatusForConfig(config)
	if !result.Enabled {
		return config, result, fmt.Errorf("remote access did not become ready: %s", result.Error)
	}
	return config, result, nil
}

func DisableRemoteAccess(config dashconfig.Config) (dashconfig.Config, error) {
	config.RemoteAccess = dashconfig.NormalizeRemoteAccess(config.RemoteAccess)
	run, _, err := remoteTailscale(config.RemoteAccess.TailscalePath)
	if err != nil {
		return config, err
	}
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
	return dashconfig.Update(func(saved *dashconfig.Config) error {
		saved.RemoteAccess.Enabled = false
		return nil
	})
}
