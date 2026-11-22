package daemon

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// refuseTunnelHost refuses what the MCP SDK's DNS rebinding check would, a
// loopback connection naming another Host, and says how a tunnel fixes it.
func refuseTunnelHost(w http.ResponseWriter, r *http.Request) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || local == nil || !mcpLoopback(local.String()) || mcpLoopback(r.Host) {
		return false
	}
	http.Error(w, fmt.Sprintf("Forbidden: invalid Host header %q. The daemon listens on %s and takes only a loopback Host there, so a tunnel or proxy in front of /mcp must send Host %s; with Cloudflare Tunnel, set originRequest.httpHostHeader: %s on the /mcp ingress rule.", r.Host, local, local, local), http.StatusForbidden)
	return true
}

func mcpLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = strings.Trim(addr, "[]")
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
