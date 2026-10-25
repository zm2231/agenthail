package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/zm2231/agenthail/internal/tailscale"
)

type tailscalePeerStatus struct {
	Self struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	Peer map[string]struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Peer"`
}

var tailnetAddress = func(dnsName string) (string, error) {
	path, err := tailscale.Executable("")
	if err != nil {
		return "", err
	}
	output, err := tailscale.Command(path)("status", "--json")
	if err != nil {
		return "", err
	}
	var status tailscalePeerStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return "", err
	}
	want := strings.TrimSuffix(strings.ToLower(dnsName), ".")
	for _, peer := range status.Peer {
		if strings.TrimSuffix(strings.ToLower(peer.DNSName), ".") != want {
			continue
		}
		for _, candidate := range peer.TailscaleIPs {
			if ip := net.ParseIP(candidate); ip != nil && ip.To4() != nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("no tailnet peer named %s", dnsName)
}
