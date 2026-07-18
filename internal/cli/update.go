package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	latestReleaseURL        = "https://api.github.com/repos/zm2231/agenthail/releases/latest"
	expectedInstallerTeamID string
)

type updateDeps struct {
	client          *http.Client
	releaseURL      string
	installerTeamID string
	executable      func() (string, error)
	run             func(string, ...string) error
	output          func(string, ...string) ([]byte, error)
	goos            string
	goarch          string
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type latestRelease struct {
	TagName string         `json:"tag_name"`
	HTMLURL string         `json:"html_url"`
	Assets  []releaseAsset `json:"assets"`
}

type updateStatus struct {
	Current       string `json:"current"`
	Latest        string `json:"latest,omitempty"`
	Available     bool   `json:"available"`
	InstallMethod string `json:"installMethod"`
	ReleaseURL    string `json:"releaseURL,omitempty"`
}

func defaultUpdateDeps() *updateDeps {
	return &updateDeps{
		client:          &http.Client{Timeout: 30 * time.Second},
		releaseURL:      latestReleaseURL,
		installerTeamID: strings.TrimSpace(expectedInstallerTeamID),
		executable:      os.Executable,
		goos:            runtime.GOOS,
		goarch:          runtime.GOARCH,
		run: func(name string, args ...string) error {
			command := exec.Command(name, args...)
			command.Stdin = os.Stdin
			command.Stdout = os.Stderr
			command.Stderr = os.Stderr
			return command.Run()
		},
		output: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		},
	}
}

func (a *App) cmdUpdate(args []string) error {
	if hasFlag(args, "--help") {
		fmt.Println("usage: agenthail update [--check] [--json]")
		return nil
	}
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail update [--check] [--json]")
	}
	deps := a.update
	if deps == nil {
		deps = defaultUpdateDeps()
	}
	executable, err := deps.executable()
	if err != nil {
		return fmt.Errorf("locate agenthail executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	method := updateInstallMethod(executable)
	if method == "homebrew" {
		return a.updateHomebrew(args, deps)
	}
	if method != "package" {
		return fmt.Errorf("self-update is available for package and Homebrew installs; update this source install with git pull and ./install.sh")
	}
	if deps.goos != "darwin" || deps.goarch != "arm64" {
		return fmt.Errorf("package updates require Apple silicon macOS")
	}
	release, err := fetchLatestRelease(deps)
	if err != nil {
		return err
	}
	status := updateStatus{
		Current:       normalizedVersion(a.Version),
		Latest:        release.TagName,
		Available:     compareVersions(release.TagName, a.Version) > 0,
		InstallMethod: method,
		ReleaseURL:    release.HTMLURL,
	}
	if hasFlag(args, "--check") || !status.Available {
		return printUpdateStatus(status, hasFlag(args, "--json"))
	}
	if err := installPackageUpdate(deps, release); err != nil {
		return err
	}
	status.Current = status.Latest
	status.Available = false
	return printUpdateStatus(status, hasFlag(args, "--json"))
}

func (a *App) updateHomebrew(args []string, deps *updateDeps) error {
	status := updateStatus{Current: normalizedVersion(a.Version), InstallMethod: "homebrew"}
	if hasFlag(args, "--check") {
		data, err := deps.output("/opt/homebrew/bin/brew", "outdated", "--json=v2", "agenthail")
		if err != nil && !exitedWithCode(err, 1) {
			return fmt.Errorf("check Homebrew update: %w", err)
		}
		var payload struct {
			Formulae []struct {
				CurrentVersion string `json:"current_version"`
			} `json:"formulae"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return fmt.Errorf("decode Homebrew update status: %w", err)
		}
		status.Available = len(payload.Formulae) > 0
		if status.Available {
			status.Latest = payload.Formulae[0].CurrentVersion
		}
		return printUpdateStatus(status, hasFlag(args, "--json"))
	}
	if err := deps.run("/opt/homebrew/bin/brew", "update"); err != nil {
		return fmt.Errorf("update Homebrew: %w", err)
	}
	if err := deps.run("/opt/homebrew/bin/brew", "upgrade", "zm2231/tap/agenthail"); err != nil {
		return fmt.Errorf("upgrade Agenthail with Homebrew: %w", err)
	}
	return printUpdateStatus(status, hasFlag(args, "--json"))
}

func exitedWithCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

func updateInstallMethod(executable string) string {
	clean := filepath.Clean(executable)
	if strings.Contains(clean, "/Cellar/agenthail/") || strings.Contains(clean, "/Homebrew/Cellar/agenthail/") {
		return "homebrew"
	}
	if strings.HasPrefix(clean, "/Library/Application Support/Agenthail/") {
		return "package"
	}
	return "source"
}

func fetchLatestRelease(deps *updateDeps) (*latestRelease, error) {
	request, err := http.NewRequest(http.MethodGet, deps.releaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "agenthail-updater")
	response, err := deps.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("check latest release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check latest release: GitHub returned %s", response.Status)
	}
	var release latestRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode latest release: %w", err)
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("latest release did not include a version")
	}
	return &release, nil
}

func installPackageUpdate(deps *updateDeps, release *latestRelease) error {
	if deps.installerTeamID == "" {
		return fmt.Errorf("package updater trust is not configured for this build")
	}
	pkgName := "Agenthail-" + release.TagName + "-arm64.pkg"
	pkgURL := assetURL(release.Assets, pkgName)
	checksumURL := assetURL(release.Assets, pkgName+".sha256")
	if pkgURL == "" || checksumURL == "" {
		return fmt.Errorf("release %s does not include the signed package and checksum", release.TagName)
	}
	directory, err := os.MkdirTemp("", "agenthail-update-")
	if err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	defer os.RemoveAll(directory)
	pkgPath := filepath.Join(directory, pkgName)
	checksumPath := pkgPath + ".sha256"
	if err := downloadAsset(deps.client, checksumURL, checksumPath, 8192); err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	if err := downloadAsset(deps.client, pkgURL, pkgPath, 300<<20); err != nil {
		return fmt.Errorf("download package: %w", err)
	}
	if err := verifyDownloadedChecksum(pkgPath, checksumPath); err != nil {
		return err
	}
	signature, err := deps.output("/usr/sbin/pkgutil", "--check-signature", pkgPath)
	if err != nil {
		return fmt.Errorf("verify package signature: %w", err)
	}
	if !strings.Contains(string(signature), "Developer ID Installer:") || !strings.Contains(string(signature), "("+deps.installerTeamID+")") {
		return fmt.Errorf("package signature is not from the configured Apple developer team")
	}
	if err := deps.run("/usr/sbin/spctl", "--assess", "--type", "install", "--verbose=2", pkgPath); err != nil {
		return fmt.Errorf("verify package notarization: %w", err)
	}
	if err := deps.run("/usr/bin/sudo", "/usr/sbin/installer", "-pkg", pkgPath, "-target", "/"); err != nil {
		return fmt.Errorf("install update from an interactive terminal with administrator access: %w", err)
	}
	return nil
}

func assetURL(assets []releaseAsset, name string) string {
	for _, asset := range assets {
		if asset.Name == name {
			return asset.URL
		}
	}
	return ""
}

func downloadAsset(client *http.Client, url, destination string, maxBytes int64) error {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "agenthail-updater")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", response.Status)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxBytes {
		return fmt.Errorf("download exceeds %d bytes", maxBytes)
	}
	return nil
}

func verifyDownloadedChecksum(pkgPath, checksumPath string) error {
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		return fmt.Errorf("read package checksum: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return fmt.Errorf("release checksum is invalid")
	}
	expected, err := hex.DecodeString(fields[0])
	if err != nil {
		return fmt.Errorf("release checksum is invalid")
	}
	file, err := os.Open(pkgPath)
	if err != nil {
		return fmt.Errorf("open downloaded package: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("hash downloaded package: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close downloaded package: %w", closeErr)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(expected)) {
		return fmt.Errorf("downloaded package checksum does not match the release")
	}
	return nil
}

func normalizedVersion(version string) string {
	if version == "" {
		return "dev"
	}
	return version
}

func compareVersions(left, right string) int {
	a, aOK := numericVersion(left)
	b, bOK := numericVersion(right)
	if !aOK || !bOK {
		if left == right {
			return 0
		}
		return 1
	}
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func numericVersion(version string) ([3]int, bool) {
	var result [3]int
	version = strings.TrimPrefix(version, "v")
	version = strings.SplitN(version, "-", 2)[0]
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return result, false
	}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return result, false
		}
		result[i] = value
	}
	return result, true
}

func printUpdateStatus(status updateStatus, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	if status.Latest == "" {
		fmt.Printf("Agenthail updated with %s\n", status.InstallMethod)
		return nil
	}
	if status.Available {
		fmt.Printf("Agenthail %s is available (installed: %s)\n", status.Latest, status.Current)
		return nil
	}
	fmt.Printf("Agenthail is up to date (%s)\n", status.Current)
	return nil
}
