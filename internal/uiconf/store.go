package uiconf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	FileName       = "ui.conf"
	backgroundsDir = "backgrounds"
	maxImageBytes  = 30 << 20
	gitTimeout     = 20 * time.Second
	initialFile    = "# Interface preferences. Every key and its default: agenthail ui show-defaults --docs\nversion = 1\n"
)

// Person is the author of hand edits and edits made in the apps.
const Person = "person"

// DefaultDir is ~/.agenthail/ui.
func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "ui")
}

// Store is the git repository that holds ui.conf. Every change is a commit
// whose author is the person or an @agent.
type Store struct {
	dir string
}

// Open returns the store at dir, creating the repository and a minimal
// ui.conf the first time.
func Open(dir string) (*Store, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("ui.conf history needs git on this host")
	}
	s := &Store{dir: dir}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return s, nil
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := s.git("init", "-q", "-b", "main"); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.Path()); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(s.Path(), []byte(initialFile), 0o600); err != nil {
			return nil, err
		}
	}
	if _, err := s.commitLocked("agenthail", "Create ui.conf"); err != nil {
		return nil, err
	}
	return s, nil
}

// Path is ui.conf's location.
func (s *Store) Path() string { return filepath.Join(s.dir, FileName) }

// Dir is the repository root.
func (s *Store) Dir() string { return s.dir }

// State is ui.conf as committed at Rev.
type State struct {
	Rev    string
	Text   string
	Parsed Parsed
}

// Current commits any hand edit first, so the state always matches a
// commit, and returns it.
func (s *Store) Current() (State, error) {
	if _, _, err := s.CommitHandEdit(); err != nil {
		return State{}, err
	}
	return s.read()
}

func (s *Store) read() (State, error) {
	text, err := os.ReadFile(s.Path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return State{}, err
	}
	rev, err := s.git("rev-parse", "--short=12", "HEAD")
	if err != nil {
		return State{}, err
	}
	return State{Rev: strings.TrimSpace(rev), Text: string(text), Parsed: Parse(string(text))}, nil
}

// Write replaces ui.conf with text and commits it. An unchanged file makes
// no commit.
func (s *Store) Write(text, author, message string) (State, bool, error) {
	if problems := Parse(text).Problems; len(problems) > 0 {
		return State{}, false, &ProblemsError{Problems: problems}
	}
	unlock, err := s.lock()
	if err != nil {
		return State{}, false, err
	}
	defer unlock()
	if _, err := s.commitLocked(Person, "Edited by hand"); err != nil {
		return State{}, false, err
	}
	if err := writeAtomic(s.Path(), []byte(text)); err != nil {
		return State{}, false, err
	}
	changed, err := s.commitLocked(author, message)
	if err != nil {
		return State{}, false, err
	}
	state, err := s.read()
	return state, changed, err
}

// Update applies change to the current text and commits the result.
func (s *Store) Update(author, message string, change func(string) string) (State, bool, error) {
	unlock, err := s.lock()
	if err != nil {
		return State{}, false, err
	}
	defer unlock()
	if _, err := s.commitLocked(Person, "Edited by hand"); err != nil {
		return State{}, false, err
	}
	current, err := os.ReadFile(s.Path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return State{}, false, err
	}
	if err := writeAtomic(s.Path(), []byte(change(string(current)))); err != nil {
		return State{}, false, err
	}
	changed, err := s.commitLocked(author, message)
	if err != nil {
		return State{}, false, err
	}
	state, err := s.read()
	return state, changed, err
}

// CommitHandEdit commits ui.conf if it differs from the last commit, as the
// person's hand edit.
func (s *Store) CommitHandEdit() (string, bool, error) {
	unlock, err := s.lock()
	if err != nil {
		return "", false, err
	}
	defer unlock()
	changed, err := s.commitLocked(Person, "Edited by hand")
	if err != nil || !changed {
		return "", changed, err
	}
	rev, err := s.git("rev-parse", "--short=12", "HEAD")
	return strings.TrimSpace(rev), true, err
}

// Entry is one commit in ui.conf's history.
type Entry struct {
	Rev     string    `json:"rev"`
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// Log returns up to limit commits, newest first.
func (s *Store) Log(limit int) ([]Entry, error) {
	out, err := s.git("log", fmt.Sprintf("-n%d", limit), "--format=%h%x1f%an%x1f%aI%x1f%s", "--abbrev=12")
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, row := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(row, "\x1f")
		if len(fields) != 4 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, fields[2])
		entries = append(entries, Entry{Rev: fields[0], Author: fields[1], Time: at, Message: fields[3]})
	}
	return entries, nil
}

// Diff shows what rev changed, or with an empty rev what the last commit
// changed.
func (s *Store) Diff(rev string) (string, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if err := s.checkRev(rev); err != nil {
		return "", err
	}
	return s.git("show", "--format=%h %an: %s%n", "--abbrev=12", rev, "--")
}

// Undo reverts rev, or the last commit when rev is empty, as a new commit.
// A revert that conflicts with later changes is abandoned and reported.
func (s *Store) Undo(rev, author string) (State, Entry, error) {
	unlock, err := s.lock()
	if err != nil {
		return State{}, Entry{}, err
	}
	defer unlock()
	if _, err := s.commitLocked(Person, "Edited by hand"); err != nil {
		return State{}, Entry{}, err
	}
	if rev == "" {
		rev = "HEAD"
	}
	if err := s.checkRev(rev); err != nil {
		return State{}, Entry{}, err
	}
	parents, err := s.git("rev-list", "--parents", "-n1", rev)
	if err != nil {
		return State{}, Entry{}, err
	}
	if len(strings.Fields(parents)) < 2 {
		return State{}, Entry{}, errors.New("the first commit can't be undone")
	}
	subject, err := s.git("log", "-n1", "--format=%s", rev)
	if err != nil {
		return State{}, Entry{}, err
	}
	reverted := strings.TrimSpace(subject)
	if _, err := s.gitAs(author, "revert", "--no-edit", "--no-commit", rev); err != nil {
		_, _ = s.git("revert", "--abort")
		_, _ = s.git("reset", "-q", "--hard", "HEAD")
		return State{}, Entry{}, fmt.Errorf("can't undo %q cleanly: a later change edited the same lines", reverted)
	}
	if _, err := s.gitAs(author, "commit", "-q", "--allow-empty", "-m", "Undo "+reverted); err != nil {
		return State{}, Entry{}, err
	}
	state, err := s.read()
	if err != nil {
		return State{}, Entry{}, err
	}
	entries, err := s.Log(1)
	if err != nil || len(entries) == 0 {
		return state, Entry{}, err
	}
	return state, entries[0], nil
}

// ImportBackground copies an image into the repository so it is versioned
// and follows the file, and returns the path to use in ui.conf.
func (s *Store) ImportBackground(source string) (string, error) {
	if err := checkImageName(source); err != nil {
		return "", err
	}
	file, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return s.ImportBackgroundData(filepath.Base(source), file)
}

// ImportBackgroundData stores an image read from body under a name that only
// supplies its extension, and returns the path to use in ui.conf.
func (s *Store) ImportBackgroundData(name string, body io.Reader) (string, error) {
	if err := checkImageName(name); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(body, maxImageBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxImageBytes {
		return "", errors.New("choose an image smaller than 30 MB")
	}
	if len(data) == 0 {
		return "", errors.New("the image is empty")
	}
	sum := sha256.Sum256(data)
	rel := filepath.Join(backgroundsDir, hex.EncodeToString(sum[:])[:16]+strings.ToLower(filepath.Ext(name)))
	if err := os.MkdirAll(filepath.Join(s.dir, backgroundsDir), 0o700); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(s.dir, rel), data); err != nil {
		return "", err
	}
	return rel, nil
}

func checkImageName(name string) error {
	if !contains(imageExtensions, strings.ToLower(filepath.Ext(name))) {
		return errors.New("background image must be " + strings.Join(imageExtensions, ", "))
	}
	return nil
}

// Asset is a background image ready to serve.
type Asset struct {
	io.ReadSeekCloser
	Name     string
	Modified time.Time
	// Tag is set for built-in pictures, which change with a release under
	// the same path.
	Tag string
}

// OpenAsset opens a background path from ui.conf: an image in the
// repository's backgrounds folder or a built-in picture. Anything else is
// refused.
func (s *Store) OpenAsset(rel string) (Asset, error) {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if filepath.IsAbs(clean) || !contains(imageExtensions, strings.ToLower(filepath.Ext(clean))) {
		return Asset{}, errAssetPath
	}
	switch filepath.Dir(clean) {
	case backgroundsDir:
		file, err := os.Open(filepath.Join(s.dir, clean))
		if err != nil {
			return Asset{}, err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return Asset{}, err
		}
		return Asset{ReadSeekCloser: file, Name: filepath.Base(clean), Modified: info.ModTime()}, nil
	case artDir:
		file, tag, err := openArt(clean)
		if err != nil {
			return Asset{}, err
		}
		seeker, ok := file.(io.ReadSeekCloser)
		if !ok {
			file.Close()
			return Asset{}, errAssetPath
		}
		return Asset{ReadSeekCloser: seeker, Name: filepath.Base(clean), Tag: tag}, nil
	}
	return Asset{}, errAssetPath
}

var errAssetPath = errors.New("only images in the ui backgrounds folder and built-in pictures are served")

// IsAssetPath reports whether err refused the path itself rather than a
// missing file.
func IsAssetPath(err error) bool { return errors.Is(err, errAssetPath) }

func (s *Store) checkRev(rev string) error {
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("unknown revision %s", rev)
	}
	if _, err := s.git("rev-parse", "--verify", "-q", rev+"^{commit}"); err != nil {
		return fmt.Errorf("unknown revision %s", rev)
	}
	return nil
}

func (s *Store) commitLocked(author, message string) (bool, error) {
	if _, err := s.git("add", "-A", "--", FileName, backgroundsDir); err != nil {
		if _, err := s.git("add", "-A", "--", FileName); err != nil {
			return false, err
		}
	}
	if _, err := s.git("rev-parse", "-q", "--verify", "HEAD"); err == nil {
		if _, err := s.git("diff", "--cached", "--quiet"); err == nil {
			return false, nil
		}
	}
	if _, err := s.gitAs(author, "commit", "-q", "--allow-empty", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) lock() (func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(s.dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(gitTimeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, errors.New("ui.conf is busy: another change is still being saved")
		}
		time.Sleep(25 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

func (s *Store) git(args ...string) (string, error) {
	return s.gitAs("agenthail", args...)
}

func (s *Store) gitAs(author string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	full := append([]string{"-C", s.dir, "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.autocrlf=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME="+author, "GIT_AUTHOR_EMAIL=ui@agenthail.invalid",
		"GIT_COMMITTER_NAME="+author, "GIT_COMMITTER_EMAIL=ui@agenthail.invalid",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
