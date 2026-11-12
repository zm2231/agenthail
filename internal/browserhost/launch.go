package browserhost

import "github.com/zm2231/agenthail/internal/nodehost"

func InstalledLaunch(version string) (Launch, error) {
	return nodehost.InstalledLaunch("browser host", "browser-host", "AGENTHAIL_BROWSER_HOST", version)
}
