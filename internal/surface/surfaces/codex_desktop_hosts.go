package surfaces

import (
	"context"
	"encoding/json"
	"time"
)

const codexHostsJS = `(async()=>{const b=globalThis.__agenthailCodexDesktopRendererV2;if(b&&typeof b.settled==='function')await b.settled();return JSON.stringify(b&&b.hosts?Object.values(b.hosts):[])})()`

type codexDesktopHost struct {
	HostID           string `json:"hostId"`
	State            string `json:"state"`
	Transport        string `json:"transport"`
	AppServerVersion string `json:"appServerVersion"`
	Fatal            string `json:"fatal"`
}

// desktopLocalHost is Desktop's own app-server connection as the bridge last
// heard it; ok is false until Desktop has reported it.
func desktopLocalHost(ctx context.Context, client codexClient) (codexDesktopHost, bool) {
	desktop, isDesktop := client.(*desktopCodexClient)
	if !isDesktop {
		return codexDesktopHost{}, false
	}
	value, err := desktop.conn.evaluate(ctx, codexHostsJS, 2*time.Second)
	text, _ := value.(string)
	var hosts []codexDesktopHost
	if err != nil || json.Unmarshal([]byte(text), &hosts) != nil {
		return codexDesktopHost{}, false
	}
	for _, host := range hosts {
		if host.HostID == "local" {
			return host, true
		}
	}
	return codexDesktopHost{}, false
}
