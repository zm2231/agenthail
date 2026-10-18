package cli

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

var archiveLayout = map[string]bool{"agenthail": true, "skills": true, "README.md": true, "LICENSE": true, "COMMERCIAL.md": true}

func linuxArchiveName(tag, goarch string) string {
	return "agenthail-" + tag + "-linux-" + goarch
}

func defaultRestartDaemon(binary string) error {
	command := exec.Command(binary, "daemon", "restart")
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	return command.Run()
}

func defaultDaemonExpected() bool {
	if _, running := daemon.IsRunning(); running {
		return true
	}
	return newDaemonService("linux").Loaded()
}

func defaultDaemonHealthy() error {
	return waitForDaemonAPI(20 * time.Second)
}

func waitForDaemonAPI(timeout time.Duration) error {
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return err
	}
	token := ""
	if config.Enabled {
		data, err := os.ReadFile(daemon.DashboardTokenPath())
		if err != nil {
			return fmt.Errorf("read dashboard token: %w", err)
		}
		token = strings.TrimSpace(string(data))
	}
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	var stableSince time.Time
	lastErr := errors.New("daemon is not running")
	for time.Now().Before(deadline) {
		if _, running := daemon.IsRunning(); !running {
			stableSince = time.Time{}
			lastErr = errors.New("daemon is not running")
		} else if !config.Enabled {
			if stableSince.IsZero() {
				stableSince = time.Now()
			} else if time.Since(stableSince) >= 2*time.Second {
				return nil
			}
		} else {
			request, _ := http.NewRequest(http.MethodGet, "http://"+config.Listen+"/api/v1/version", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response, err := client.Do(request)
			if err == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
				lastErr = fmt.Errorf("daemon API returned %s", response.Status)
			} else {
				lastErr = err
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not answer on its API within %s: %w", timeout, lastErr)
}

func installArchiveUpdate(deps *updateDeps, release *latestRelease, executable string) error {
	dataDir := filepath.Dir(executable)
	if err := verifyArchiveLayout(dataDir); err != nil {
		return err
	}
	name := linuxArchiveName(release.TagName, deps.goarch)
	archiveURL := assetURL(release.Assets, name+".tar.gz")
	checksumURL := assetURL(release.Assets, name+".tar.gz.sha256")
	if archiveURL == "" || checksumURL == "" {
		return fmt.Errorf("release %s does not include %s.tar.gz and its checksum", release.TagName, name)
	}
	work, err := os.MkdirTemp(filepath.Dir(dataDir), ".agenthail-update-")
	if err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	defer os.RemoveAll(work)
	archivePath := filepath.Join(work, name+".tar.gz")
	checksumPath := archivePath + ".sha256"
	if err := downloadAsset(deps.client, checksumURL, checksumPath, 8192); err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	if err := downloadAsset(deps.client, archiveURL, archivePath, 300<<20); err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	if err := verifyDownloadedChecksum(archivePath, checksumPath); err != nil {
		return err
	}
	stage := filepath.Join(work, "stage")
	if err := extractReleaseArchive(archivePath, stage, name); err != nil {
		return err
	}
	next := filepath.Join(stage, name)
	output, err := deps.output(filepath.Join(next, "agenthail"), "version", "--json")
	if err != nil {
		return fmt.Errorf("run downloaded agenthail: %w", err)
	}
	var stamped struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(output, &stamped); err != nil || stamped.Version != release.TagName {
		return fmt.Errorf("downloaded agenthail reports version %q, want %s", stamped.Version, release.TagName)
	}
	restart := deps.daemonExpected()
	previous := dataDir + ".previous"
	if err := os.RemoveAll(previous); err != nil {
		return fmt.Errorf("clear %s: %w", previous, err)
	}
	if err := os.Rename(dataDir, previous); err != nil {
		return fmt.Errorf("move current install aside: %w", err)
	}
	if err := os.Rename(next, dataDir); err != nil {
		if restoreErr := os.Rename(previous, dataDir); restoreErr != nil {
			return fmt.Errorf("activate update: %w; restore %s from %s by hand: %v", err, dataDir, previous, restoreErr)
		}
		return fmt.Errorf("activate update: %w", err)
	}
	if restart {
		binary := filepath.Join(dataDir, "agenthail")
		if err := restartAndCheck(deps, binary); err != nil {
			return rollbackArchiveUpdate(deps, dataDir, previous, release.TagName, err)
		}
	}
	return os.RemoveAll(previous)
}

func restartAndCheck(deps *updateDeps, binary string) error {
	if err := deps.restartDaemon(binary); err != nil {
		return fmt.Errorf("restart daemon: %w", err)
	}
	return deps.daemonHealthy()
}

func rollbackArchiveUpdate(deps *updateDeps, dataDir, previous, tag string, cause error) error {
	failed := dataDir + ".failed"
	if err := os.RemoveAll(failed); err != nil {
		return fmt.Errorf("update to %s failed (%v); could not clear %s: %v; the previous install is at %s", tag, cause, failed, err, previous)
	}
	if err := os.Rename(dataDir, failed); err != nil {
		return fmt.Errorf("update to %s failed (%v); could not move it aside: %v; the previous install is at %s", tag, cause, err, previous)
	}
	if err := os.Rename(previous, dataDir); err != nil {
		return fmt.Errorf("update to %s failed (%v); could not restore %s from %s: %v", tag, cause, dataDir, previous, err)
	}
	if err := restartAndCheck(deps, filepath.Join(dataDir, "agenthail")); err != nil {
		return fmt.Errorf("update to %s failed (%v); restored the previous install, but its daemon did not come back: %v", tag, cause, err)
	}
	os.RemoveAll(failed)
	return fmt.Errorf("update to %s failed and was rolled back; the previous version is running again: %w", tag, cause)
}

func verifyArchiveLayout(dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return fmt.Errorf("read install directory: %w", err)
	}
	for _, entry := range entries {
		if !archiveLayout[entry.Name()] {
			return fmt.Errorf("%s holds %s, which is not part of the Agenthail release archive; reinstall from the release archive into its own directory before updating", dataDir, entry.Name())
		}
	}
	return nil
}

func extractReleaseArchive(archivePath, destination, root string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		clean := filepath.Clean(header.Name)
		if filepath.IsAbs(clean) || (clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator))) {
			return fmt.Errorf("archive entry %q is outside %s", header.Name, root)
		}
		target := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += header.Size
			if total > 600<<20 {
				return fmt.Errorf("archive expands beyond %d bytes", 600<<20)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, io.LimitReader(reader, header.Size))
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type %c", header.Name, header.Typeflag)
		}
	}
	if !isExecutable(filepath.Join(destination, root, "agenthail")) {
		return fmt.Errorf("archive does not contain %s/agenthail", root)
	}
	return nil
}
