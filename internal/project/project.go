// Package project places a working directory in the Git repository that
// contains it or, outside any repository, the nearest registered folder.
package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/workspace"
)

const (
	KindGit    = "git"
	KindFolder = "folder"
)

const GitTimeout = 3 * time.Second

type Project struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	RootPath    string `json:"rootPath"`
	Kind        string `json:"kind"`
	Archived    bool   `json:"archived,omitempty"`
}

// Of resolves the project of a working directory, or nil when it is in none.
// A git failure other than "not a git repository" is returned, since the
// directory may still belong to a repository.
func Of(ctx context.Context, cwd string, records []registry.ProjectRecord) (*Project, error) {
	path, err := SessionPath(cwd)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("no working directory")
	}
	repository, notRepository, err := ReadRepository(ctx, NearestExistingDirectory(path))
	switch {
	case err == nil:
		return repository.Project(), nil
	case notRepository:
		return NearestFolder(path, Folders(records)), nil
	}
	return nil, err
}

func Folders(records []registry.ProjectRecord) []registry.ProjectRecord {
	folders := make([]registry.ProjectRecord, 0, len(records))
	for _, record := range records {
		if record.Registered && record.Kind == KindFolder {
			folders = append(folders, record)
		}
	}
	return folders
}

type Repository struct {
	Toplevel  string
	CommonDir string
	GitDir    string
	Root      string
}

func (repository Repository) Project() *Project {
	return &Project{ID: PathID("project", repository.CommonDir), DisplayName: Name(repository.Root), RootPath: repository.Root, Kind: KindGit}
}

// ReadRepository reads the checkout root, the shared repository directory
// and the checkout's own git directory. Each is its own git call because a
// path may contain a newline. The project root is the main checkout: the
// parent of a ".git" common directory, the checkout itself when it owns its
// git directory (a submodule), and the common directory of a bare repository.
func ReadRepository(ctx context.Context, path string) (repository Repository, notRepository bool, err error) {
	gitCtx, cancel := context.WithTimeout(ctx, GitTimeout)
	defer cancel()
	toplevel, err := Git(gitCtx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return Repository{}, IsNotRepository(err), err
	}
	if repository.Toplevel, err = Canonical(toplevel); err != nil {
		return Repository{}, false, err
	}
	for _, read := range []struct {
		target *string
		args   []string
	}{
		{&repository.CommonDir, []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}},
		{&repository.GitDir, []string{"rev-parse", "--absolute-git-dir"}},
	} {
		value, err := Git(gitCtx, repository.Toplevel, read.args...)
		if err != nil {
			return Repository{}, false, err
		}
		if *read.target, err = Canonical(value); err != nil {
			return Repository{}, false, err
		}
	}
	switch {
	case filepath.Base(repository.CommonDir) == ".git":
		repository.Root = filepath.Dir(repository.CommonDir)
	case repository.GitDir == repository.CommonDir:
		repository.Root = repository.Toplevel
	default:
		repository.Root = repository.CommonDir
	}
	return repository, false, nil
}

func NearestFolder(path string, folders []registry.ProjectRecord) *Project {
	var nearest *registry.ProjectRecord
	for index := range folders {
		within, err := workspace.IsWithin(folders[index].RootPath, path)
		if err != nil || !within {
			continue
		}
		if nearest == nil || len(folders[index].RootPath) > len(nearest.RootPath) {
			nearest = &folders[index]
		}
	}
	if nearest == nil {
		return nil
	}
	return &Project{ID: nearest.ID, DisplayName: nearest.DisplayName, RootPath: nearest.RootPath, Kind: KindFolder}
}

func Name(root string) string {
	name := filepath.Base(root)
	if trimmed := strings.TrimSuffix(name, ".git"); trimmed != "" {
		return trimmed
	}
	return name
}

// NearestExistingDirectory returns path, or its closest ancestor that still
// exists. A session whose worktree was removed keeps the project of the
// repository the worktree lived in.
func NearestExistingDirectory(path string) string {
	for candidate := path; ; candidate = filepath.Dir(candidate) {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		if parent := filepath.Dir(candidate); parent == candidate {
			return candidate
		}
	}
}

// SessionPath makes a discovered working directory absolute and canonical.
// Unlike typed input, its bytes are exact, so nothing is trimmed. A directory
// that no longer exists is canonical through its nearest existing ancestor,
// so a removed worktree keeps the same path form as its repository.
func SessionPath(cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	existing := NearestExistingDirectory(abs)
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return abs, nil
	}
	rest, err := filepath.Rel(existing, abs)
	if err != nil {
		return abs, nil
	}
	return filepath.Join(resolved, rest), nil
}

func Canonical(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved), nil
	}
	if _, err := os.Stat(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func PathID(prefix, path string) string {
	digest := sha256.Sum256([]byte(path))
	return prefix + "-" + hex.EncodeToString(digest[:12])
}

func Git(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", &gitError{args: args, stderr: stderr.String(), err: err}
	}
	return strings.TrimSuffix(string(output), "\n"), nil
}

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %s", strings.Join(e.args, " "), strings.TrimSpace(e.stderr+" "+e.err.Error()))
}

func (e *gitError) Unwrap() error { return e.err }

// IsNotRepository reports git's answer that the folder is outside any
// repository. Other failures, such as unreadable metadata or a dubious
// ownership refusal, leave the directory's project unknown.
func IsNotRepository(err error) bool {
	var gitErr *gitError
	return errors.As(err, &gitErr) && strings.Contains(gitErr.stderr, "not a git repository")
}
