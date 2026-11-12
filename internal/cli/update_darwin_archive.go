package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	if err := deps.runInstaller(filepath.Join(next, "install.sh"), []string{"AGENTHAIL_DATA_DIR=" + dataDir}); err != nil {
		return fmt.Errorf("install %s: %w; the installer restores the previous install when it fails", release.TagName, err)
	}
	if deps.daemonExpected() {
		if err := deps.daemonHealthy(); err != nil {
			return fmt.Errorf("installed %s, but its daemon is not answering: %w; see ~/.agenthail/daemon.log", release.TagName, err)
		}
	}
	return nil
}
