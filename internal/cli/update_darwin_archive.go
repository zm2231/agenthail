package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var darwinArchiveMarkers = []string{"Agenthail.app", "sidecar.py"}

func isDarwinArchiveInstall(dataDir string) bool {
	for _, marker := range darwinArchiveMarkers {
		if _, err := os.Stat(filepath.Join(dataDir, marker)); err != nil {
			return false
		}
	}
	return true
}

func defaultSigningTeam(path string) (string, error) {
	if output, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", path).CombinedOutput(); err != nil {
		return "", fmt.Errorf("the signature of %s does not verify: %s", path, strings.TrimSpace(string(output)))
	}
	output, err := exec.Command("/usr/bin/codesign", "-dv", "--verbose=2", path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read the signature of %s: %w", path, err)
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if team, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier="); ok && team != "not set" {
			return team, nil
		}
	}
	return "", fmt.Errorf("%s is not signed by a developer team", path)
}

func defaultRunInstaller(script string, env []string) error {
	command := exec.Command(script)
	command.Dir = filepath.Dir(script)
	command.Env = append(os.Environ(), env...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	return command.Run()
}

func installDarwinArchiveUpdate(deps *updateDeps, release *latestRelease, executable string) error {
	dataDir := filepath.Dir(executable)
	installed, err := deps.signingTeam(executable)
	if err != nil {
		return fmt.Errorf("refusing to update an install whose signer cannot be read: %w", err)
	}
	work, err := os.MkdirTemp(filepath.Dir(dataDir), ".agenthail-update-")
	if err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	defer os.RemoveAll(work)
	next, err := fetchReleaseArchive(deps, release, releaseArchiveName(release.TagName, deps.goos, deps.goarch), work)
	if err != nil {
		return err
	}
	for _, signed := range []string{filepath.Join(next, "agenthail"), filepath.Join(next, "Agenthail.app")} {
		team, err := deps.signingTeam(signed)
		if err != nil {
			return fmt.Errorf("verify the downloaded release: %w", err)
		}
		if team != installed {
			return fmt.Errorf("the downloaded %s is signed by team %s, not %s like this install", filepath.Base(signed), team, installed)
		}
	}
	previous := filepath.Join(work, "previous")
	if err := deps.copyTree(dataDir, previous); err != nil {
		return fmt.Errorf("keep a copy of the current install to roll back to: %w", err)
	}
	wrapper, wrapperBody, named := managedWrapper(dataDir, deps.wrapperDirs())
	env := []string{"AGENTHAIL_DATA_DIR=" + dataDir}
	if wrapper != "" {
		env = []string{"AGENTHAIL_DATA_DIR=" + named, "AGENTHAIL_INSTALL_DIR=" + filepath.Dir(wrapper)}
	}
	running := deps.daemonExpected()
	rollback := func(cause error) error {
		failed := dataDir + ".failed"
		if err := os.RemoveAll(failed); err != nil {
			return fmt.Errorf("update to %s failed (%v); could not clear %s: %v; the previous install is at %s", release.TagName, cause, failed, err, previous)
		}
		if err := os.Rename(dataDir, failed); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("update to %s failed (%v); could not move it aside: %v; the previous install is at %s", release.TagName, cause, err, previous)
		}
		if err := os.Rename(previous, dataDir); err != nil {
			return fmt.Errorf("update to %s failed (%v); could not restore %s from %s: %v", release.TagName, cause, dataDir, previous, err)
		}
		os.RemoveAll(failed)
		if wrapper != "" {
			if err := os.WriteFile(wrapper, wrapperBody, 0755); err != nil {
				return fmt.Errorf("update to %s failed (%v); restored the previous install, but not its command at %s: %v", release.TagName, cause, wrapper, err)
			}
		}
		if running {
			if err := restartAndCheck(deps, filepath.Join(dataDir, "agenthail")); err != nil {
				return fmt.Errorf("update to %s failed (%v); restored the previous install, but its daemon did not come back: %v", release.TagName, cause, err)
			}
		}
		return fmt.Errorf("update to %s failed and was rolled back; the previous version is in place again: %w", release.TagName, cause)
	}
	if err := deps.runInstaller(filepath.Join(next, "install.sh"), env); err != nil {
		return rollback(fmt.Errorf("the release's installer: %w", err))
	}
	if running {
		if err := deps.daemonHealthy(); err != nil {
			return rollback(fmt.Errorf("its daemon is not answering: %w", err))
		}
	}
	return nil
}

func defaultCopyTree(source, destination string) error {
	if err := exec.Command("/bin/cp", "-cRp", source, destination).Run(); err == nil {
		return nil
	}
	os.RemoveAll(destination)
	if output, err := exec.Command("/bin/cp", "-Rp", source, destination).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func defaultWrapperDirs() []string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
}

func managedWrapper(dataDir string, dirs []string) (string, []byte, string) {
	installed, err := os.Stat(filepath.Join(dataDir, "agenthail"))
	if err != nil {
		return "", nil, ""
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "agenthail")
		body, err := os.ReadFile(path)
		if err != nil || len(body) > 64<<10 || !strings.Contains(string(body), "agenthail-managed-wrapper") {
			continue
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			rest, ok := strings.CutPrefix(strings.TrimSpace(line), "exec ")
			if !ok {
				continue
			}
			if target, ok := shellWord(rest); ok {
				if found, err := os.Stat(target); err == nil && os.SameFile(found, installed) {
					return path, body, filepath.Dir(target)
				}
			}
		}
	}
	return "", nil, ""
}

func shellWord(text string) (string, bool) {
	var word strings.Builder
	for i := 0; i < len(text); {
		switch c := text[i]; {
		case c == ' ' || c == '\t':
			return word.String(), word.Len() > 0
		case c == '\\':
			if i+1 >= len(text) {
				return "", false
			}
			word.WriteByte(text[i+1])
			i += 2
		case c == '\'':
			end := strings.IndexByte(text[i+1:], '\'')
			if end < 0 {
				return "", false
			}
			word.WriteString(text[i+1 : i+1+end])
			i += end + 2
		case c == '"':
			i++
			for ; i < len(text) && text[i] != '"'; i++ {
				if text[i] == '\\' && i+1 < len(text) && strings.IndexByte("$`\"\\", text[i+1]) >= 0 {
					i++
				}
				word.WriteByte(text[i])
			}
			if i >= len(text) {
				return "", false
			}
			i++
		case c == '$' && i+1 < len(text) && text[i+1] == '\'':
			n, ok := ansiCQuoted(text[i+2:], &word)
			if !ok {
				return "", false
			}
			i += 2 + n
		default:
			word.WriteByte(c)
			i++
		}
	}
	return word.String(), word.Len() > 0
}

func ansiCQuoted(text string, word *strings.Builder) (int, bool) {
	escapes := map[byte]byte{'n': '\n', 't': '\t', 'r': '\r', 'a': '\a', 'b': '\b', 'f': '\f', 'v': '\v', 'e': 0x1b, 'E': 0x1b, '\\': '\\', '\'': '\'', '"': '"', '?': '?'}
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '\'':
			return i + 1, true
		case c != '\\' || i+1 >= len(text):
			word.WriteByte(c)
		case escapes[text[i+1]] != 0:
			word.WriteByte(escapes[text[i+1]])
			i++
		case text[i+1] == 'x':
			j := i + 2
			for j < len(text) && j < i+4 && strings.IndexByte("0123456789abcdefABCDEF", text[j]) >= 0 {
				j++
			}
			value, err := strconv.ParseUint(text[i+2:j], 16, 8)
			if err != nil {
				return 0, false
			}
			word.WriteByte(byte(value))
			i = j - 1
		case text[i+1] >= '0' && text[i+1] <= '7':
			j := i + 1
			for j < len(text) && j < i+4 && text[j] >= '0' && text[j] <= '7' {
				j++
			}
			value, err := strconv.ParseUint(text[i+1:j], 8, 8)
			if err != nil {
				return 0, false
			}
			word.WriteByte(byte(value))
			i = j - 1
		default:
			word.WriteByte(c)
		}
	}
	return 0, false
}
