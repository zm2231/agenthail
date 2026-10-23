package sidecar

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/zm2231/agenthail/internal/testnode"
)

type bridgeHome struct {
	home   string
	path   string
	safari []string
}

func requireCookieBridgeDeps(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the browser fixtures use macOS profile locations")
	}
	testnode.Path(t)
	if _, err := os.Stat(filepath.Join("node_modules", "@steipete", "sweet-cookie")); err != nil {
		if os.Getenv("CI") == "" {
			t.Skip("run `npm ci --ignore-scripts` in sidecar/ to test the cookie bridge")
		}
		t.Fatal("CI must run `npm ci --ignore-scripts` in sidecar/ before the cookie bridge tests")
	}
}

func newBridgeHome(t *testing.T) bridgeHome {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	security := "#!/bin/sh\necho fixture-safe-storage\n"
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte(security), 0700); err != nil {
		t.Fatal(err)
	}
	fixture := bridgeHome{home: home, path: bin + string(os.PathListSeparator) + filepath.Dir(nodePath(t))}
	for _, path := range []string{
		filepath.Join(home, "Library", "Cookies", "Cookies.binarycookies"),
		filepath.Join(home, "Library", "Containers", "com.apple.Safari", "Data", "Library", "Cookies", "Cookies.binarycookies"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		fixture.safari = append(fixture.safari, path)
	}
	t.Cleanup(func() {
		for _, path := range fixture.safari {
			if writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				writer.Close()
			}
		}
	})
	return fixture
}

func nodePath(t *testing.T) string {
	t.Helper()
	return testnode.Path(t)
}

func writeChromeCookieDB(t *testing.T, profileDir, host, name, value string) {
	t.Helper()
	path := filepath.Join(profileDir, "Network", "Cookies")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE meta(key TEXT NOT NULL UNIQUE PRIMARY KEY, value TEXT);
		INSERT INTO meta VALUES('version','23');
		CREATE TABLE cookies(host_key TEXT NOT NULL, name TEXT NOT NULL, value TEXT NOT NULL, path TEXT NOT NULL,
			expires_utc INTEGER NOT NULL, is_secure INTEGER NOT NULL, is_httponly INTEGER NOT NULL,
			samesite INTEGER NOT NULL DEFAULT -1, encrypted_value BLOB DEFAULT x'');
		INSERT INTO cookies(host_key,name,value,path,expires_utc,is_secure,is_httponly) VALUES(?,?,?,'/',0,1,1);`, host, name, value)
	if err != nil {
		t.Fatal(err)
	}
}

func runBridge(t *testing.T, fixture bridgeHome, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, testnode.Path(t), append([]string{"cookie.mjs"}, args...)...)
	cmd.Env = []string{"HOME=" + fixture.home, "PATH=" + fixture.path, "TMPDIR=" + t.TempDir()}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("cookie bridge did not finish: stderr=%s", stderr.String())
	}
	return stdout.String(), stderr.String(), err
}

func TestCookieBridgeReadsOnlyTheChosenChromeProfile(t *testing.T) {
	requireCookieBridgeDeps(t)
	fixture := newBridgeHome(t)
	root := filepath.Join(fixture.home, "Library", "Application Support", "Google", "Chrome")
	writeChromeCookieDB(t, filepath.Join(root, "Profile 2"), ".example.test", "session", "chosen")
	writeChromeCookieDB(t, filepath.Join(root, "Default"), ".example.test", "session", "other")
	brave := filepath.Join(fixture.home, "Library", "Application Support", "BraveSoftware", "Brave-Browser", "Default")
	writeChromeCookieDB(t, brave, ".example.test", "session", "brave")

	header, stderr, err := runBridge(t, fixture, "--browser", "chrome", "--profile-dir", filepath.Join(root, "Profile 2"), "https://app.example.test/")
	if err != nil || header != "session=chosen" {
		t.Fatalf("header=%q err=%v stderr=%s", header, err, stderr)
	}
}

func TestCookieBridgeFailsWithoutReadingAnotherBrowser(t *testing.T) {
	requireCookieBridgeDeps(t)
	fixture := newBridgeHome(t)
	root := filepath.Join(fixture.home, "Library", "Application Support", "Google", "Chrome")
	writeChromeCookieDB(t, filepath.Join(root, "Default"), ".other.test", "session", "unrelated")

	header, stderr, err := runBridge(t, fixture, "--browser", "chrome", "--profile-dir", filepath.Join(root, "Default"), "https://app.example.test/")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || header != "" || !strings.Contains(stderr, "no chrome cookies") {
		t.Fatalf("header=%q err=%v stderr=%s", header, err, stderr)
	}
}

func TestCookieBridgeRequiresAnExplicitBrowser(t *testing.T) {
	requireCookieBridgeDeps(t)
	fixture := newBridgeHome(t)
	for _, args := range [][]string{
		{"https://app.example.test/"},
		{"--browser", "chrome", "https://app.example.test/"},
		{"--browser", "opera", "--profile-dir", fixture.home, "https://app.example.test/"},
	} {
		_, stderr, err := runBridge(t, fixture, args...)
		if err == nil || !strings.Contains(stderr, "cookie read error") {
			t.Fatalf("args=%v err=%v stderr=%s", args, err, stderr)
		}
	}
}
