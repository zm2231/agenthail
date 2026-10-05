package surfaces

import (
	"os"
	"path/filepath"
	"strings"
)

const CodexStandaloneInstallCommand = "curl -fsSL https://chatgpt.com/codex/install.sh | sh"

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

var codexBundledCLIs = func() []string {
	userHome, _ := os.UserHomeDir()
	return []string{
		"/Applications/ChatGPT.app/Contents/Resources/codex",
		filepath.Join(userHome, "Applications", "ChatGPT.app", "Contents", "Resources", "codex"),
	}
}

// Only Standalone can run the managed app-server daemon; Codex refuses to
// start it from any other copy.
type CodexInstallation struct {
	Standalone string   `json:"standalone,omitempty"`
	Desktop    string   `json:"desktop,omitempty"`
	Other      []string `json:"other,omitempty"`
}

func codexHome() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	userHome, _ := os.UserHomeDir()
	return filepath.Join(userHome, ".codex")
}

func codexStandalonePath() string {
	return filepath.Join(codexHome(), "packages", "standalone", "current", "codex")
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}

func DetectCodexInstallation() CodexInstallation {
	var install CodexInstallation
	if standalone := codexStandalonePath(); isExecutableFile(standalone) {
		install.Standalone = standalone
	}
	for _, path := range CodexDesktopExecutables {
		if isExecutableFile(path) {
			install.Desktop = path
			break
		}
	}
	seen := map[string]bool{}
	if install.Standalone != "" {
		if resolved, err := filepath.EvalSymlinks(install.Standalone); err == nil {
			seen[resolved] = true
		}
	}
	candidates := []string{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" {
			candidates = append(candidates, filepath.Join(dir, "codex"))
		}
	}
	candidates = append(candidates, codexBundledCLIs()...)
	for _, path := range candidates {
		if !isExecutableFile(path) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		install.Other = append(install.Other, path)
	}
	return install
}

func (install CodexInstallation) standaloneMissingNote() string {
	message := "managed Codex terminals ('agenthail codex') need the standalone Codex runtime at " + codexStandalonePath()
	if len(install.Other) > 0 {
		message += "; found " + strings.Join(install.Other, ", ") + ", which cannot run the managed app-server"
	}
	return message
}
