package surfaces

import (
	"os"
	"path/filepath"
	"strings"
)

const CodexInstallCommand = "curl -fsSL https://chatgpt.com/codex/install.sh | sh"

var CodexDesktopExecutables = codexDesktopExecutables()

func codexDesktopExecutables() []string {
	userHome, _ := os.UserHomeDir()
	paths := []string{}
	for _, root := range []string{"/Applications", filepath.Join(userHome, "Applications")} {
		paths = append(paths,
			filepath.Join(root, "ChatGPT.app", "Contents", "MacOS", "ChatGPT"),
			filepath.Join(root, "Codex.app", "Contents", "MacOS", "ChatGPT"),
			filepath.Join(root, "Codex.app", "Contents", "MacOS", "Codex"),
		)
	}
	return paths
}

// CodexInstallation names the Codex CLI that runs `codex app-server daemon`
// lifecycle commands and the Codex Desktop app, when present. Codex decides
// whether that CLI can start its daemon; Agenthail only picks which CLI asks.
type CodexInstallation struct {
	Daemon  string `json:"daemon,omitempty"`
	Desktop string `json:"desktop,omitempty"`
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}

func isCodexDesktopBundle(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	return strings.Contains(resolved, ".app/Contents/")
}

func DetectCodexInstallation(home CodexHome) CodexInstallation {
	var install CodexInstallation
	for _, path := range CodexDesktopExecutables {
		if isExecutableFile(path) {
			install.Desktop = path
			break
		}
	}
	candidates := home.daemonPackageBinaries()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" {
			candidates = append(candidates, filepath.Join(dir, "codex"))
		}
	}
	for _, path := range candidates {
		if isExecutableFile(path) && !isCodexDesktopBundle(path) {
			install.Daemon = path
			break
		}
	}
	return install
}
