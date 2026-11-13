package uiconf

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/gitstore"
)

const (
	FileName       = "ui.conf"
	backgroundsDir = "backgrounds"
	maxImageBytes  = 30 << 20
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
	dir  string
	repo *gitstore.Repo
}

// Open returns the store at dir, creating the repository and a minimal
// ui.conf the first time.
func Open(dir string) (*Store, error) {
	if err := gitstore.Available(); err != nil {
		return nil, errors.New("ui.conf history needs git on this host")
	}
	s := &Store{dir: dir, repo: gitstore.New(dir, "ui@agenthail.invalid", "ui.conf is busy: another change is still being saved")}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if s.repo.Initialized() {
		return s, nil
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.repo.InitLocked(); err != nil {
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
	rev, err := s.repo.Head()
	if err != nil {
		return State{}, err
	}
	return State{Rev: rev, Text: string(text), Parsed: Parse(string(text))}, nil
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
	if err := atomicfile.Write(s.Path(), []byte(text), 0o600); err != nil {
		return State{}, false, err
	}
	changed, err := s.commitLocked(author, message)
	if err != nil {
		return State{}, false, err
	}
	state, err := s.read()
	return state, changed, err
}

// Update applies change to the current text and commits the result with the
// message change returns.
func (s *Store) Update(author string, change func(string) (text, message string)) (State, bool, error) {
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
	text, message := change(string(current))
	if err := atomicfile.Write(s.Path(), []byte(text), 0o600); err != nil {
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
	rev, err := s.repo.Head()
	return rev, true, err
}

// Entry is one commit in ui.conf's history.
type Entry = gitstore.Entry

// Log returns up to limit commits, newest first.
func (s *Store) Log(limit int) ([]Entry, error) {
	return s.repo.Log(limit)
}

// Diff shows what rev changed, or with an empty rev what the last commit
// changed.
func (s *Store) Diff(rev string) (string, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if err := s.repo.CheckRev(rev); err != nil {
		return "", err
	}
	return s.repo.Git("show", "--format=%h %an: %s%n", "--abbrev=12", rev, "--")
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
	if _, err := s.repo.RevertLocked(rev, author); err != nil {
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

var errImageTooLarge = errors.New("choose an image smaller than 30 MB")

// CheckBackgroundUpload refuses an upload by its name and declared size
// before any of it is read. A negative size is unknown and passes.
func CheckBackgroundUpload(name string, size int64) error {
	if err := checkImageName(name); err != nil {
		return err
	}
	if size > maxImageBytes {
		return errImageTooLarge
	}
	return nil
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
		return "", errImageTooLarge
	}
	if len(data) == 0 {
		return "", errors.New("the image is empty")
	}
	sum := sha256.Sum256(data)
	rel := filepath.Join(backgroundsDir, hex.EncodeToString(sum[:])[:16]+strings.ToLower(filepath.Ext(name)))
	if err := os.MkdirAll(filepath.Join(s.dir, backgroundsDir), 0o700); err != nil {
		return "", err
	}
	if err := atomicfile.Write(filepath.Join(s.dir, rel), data, 0o600); err != nil {
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

func (s *Store) commitLocked(author, message string) (bool, error) {
	if err := s.repo.Stage(FileName, backgroundsDir); err != nil {
		if err := s.repo.Stage(FileName); err != nil {
			return false, err
		}
	}
	return s.repo.CommitStaged(author, message)
}

func (s *Store) lock() (func(), error) { return s.repo.Lock() }
