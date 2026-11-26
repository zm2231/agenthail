package simhost

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type entry struct {
	name string
	flag byte
	size int64
	body string
	link string
}

func uploadOf(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	compressed := gzip.NewWriter(&out)
	archive := tar.NewWriter(compressed)
	for _, e := range entries {
		size := e.size
		if size == 0 {
			size = int64(len(e.body))
		}
		if err := archive.WriteHeader(&tar.Header{Name: e.name, Typeflag: e.flag, Size: size, Linkname: e.link, Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := archive.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	archive.Flush()
	compressed.Close()
	return &out
}

func installRefusal(t *testing.T, h *Host, upload io.Reader) string {
	t.Helper()
	_, err := h.InstallArchive(context.Background(), "s1", Request{Action: "install"}, upload)
	var refusal *Error
	if !errors.As(err, &refusal) {
		t.Fatalf("install = %v, want a refusal", err)
	}
	return refusal.Code
}

func stagedCopies(t *testing.T, h *Host) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(h.outRoot, ".staging", "install-*"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}

func TestAnUploadThatIsNotOneSafeAppInstallsNothing(t *testing.T) {
	h, sim := leased(t)
	dir := entry{name: "Rune.app/", flag: tar.TypeDir}
	cases := map[string]*bytes.Buffer{
		"parent path":     uploadOf(t, dir, entry{name: "../evil", flag: tar.TypeReg, body: "x"}),
		"escape in app":   uploadOf(t, dir, entry{name: "Rune.app/../../evil", flag: tar.TypeReg, body: "x"}),
		"absolute path":   uploadOf(t, entry{name: "/tmp/Rune.app/", flag: tar.TypeDir}),
		"backslash":       uploadOf(t, dir, entry{name: `Rune.app\..\evil`, flag: tar.TypeReg, body: "x"}),
		"link":            uploadOf(t, dir, entry{name: "Rune.app/Rune", flag: tar.TypeSymlink, link: "/etc/passwd"}),
		"hard link":       uploadOf(t, dir, entry{name: "Rune.app/Rune", flag: tar.TypeLink, link: "Rune.app/Info.plist"}),
		"two apps":        uploadOf(t, dir, entry{name: "Other.app/", flag: tar.TypeDir}),
		"not an app":      uploadOf(t, entry{name: "Rune/", flag: tar.TypeDir}),
		"app is a file":   uploadOf(t, entry{name: "Rune.app", flag: tar.TypeReg, body: "x"}),
		"empty":           uploadOf(t),
		"not gzip at all": bytes.NewBufferString("plain text"),
	}
	for name, upload := range cases {
		if code := installRefusal(t, h, upload); code != "invalid" {
			t.Errorf("%s: install = %s, want invalid", name, code)
		}
	}
	if len(sim.ran("xcrun simctl install")) != 0 {
		t.Fatal("a refused upload reached simctl")
	}
	if left := stagedCopies(t, h); len(left) != 0 {
		t.Fatalf("refused uploads left %v", left)
	}
	if _, err := os.Stat(filepath.Join(h.outRoot, ".staging", "evil")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an unsafe path was written outside the copy")
	}
}

func TestAnUploadOverTheLimitIsRefused(t *testing.T) {
	h, sim := leased(t)
	upload := uploadOf(t, entry{name: "Rune.app/", flag: tar.TypeDir}, entry{name: "Rune.app/huge", flag: tar.TypeReg, size: AppArchiveLimit + 1})
	if code := installRefusal(t, h, upload); code != "too_large" {
		t.Fatalf("oversized upload = %s", code)
	}
	if len(sim.ran("xcrun simctl install")) != 0 || len(stagedCopies(t, h)) != 0 {
		t.Fatal("an oversized upload was installed or kept")
	}
}

func TestALeaseTakesOneUploadAtATime(t *testing.T) {
	h, sim := leased(t)
	first, feed := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := h.InstallArchive(context.Background(), "s1", Request{Action: "install"}, first)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(stagedCopies(t, h)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if code := installRefusal(t, h, uploadOf(t, entry{name: "Rune.app/", flag: tar.TypeDir})); code != "busy" {
		t.Fatalf("a second upload on the lease = %s", code)
	}
	if _, err := feed.Write(uploadOf(t, entry{name: "Rune.app/", flag: tar.TypeDir}, entry{name: "Rune.app/Rune", flag: tar.TypeReg, body: "bin"}).Bytes()); err != nil {
		t.Fatal(err)
	}
	feed.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(sim.ran("xcrun simctl install U1")) != 1 || len(stagedCopies(t, h)) != 0 {
		t.Fatalf("the first upload = %+v, left %v", sim.ran("xcrun simctl install"), stagedCopies(t, h))
	}
	if _, err := h.InstallArchive(context.Background(), "s1", Request{Action: "install"}, uploadOf(t, entry{name: "Rune.app/", flag: tar.TypeDir})); err != nil {
		t.Fatalf("an upload after the first ended = %v", err)
	}
}

func TestStartingRemovesOnlyInstallCopiesLeftBehind(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".staging/install-123/Rune.app", ".staging/keep", "abcdef/Mine.app"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	New(Config{OutRoot: root, Exec: newFakeSim().exec})
	if _, err := os.Stat(filepath.Join(root, ".staging", "install-123")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a leftover install copy survived the start")
	}
	for _, dir := range []string{".staging/keep", "abcdef/Mine.app"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
			t.Fatalf("the start removed %s: %v", dir, err)
		}
	}
}
