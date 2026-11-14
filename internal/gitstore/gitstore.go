// Package gitstore keeps a folder Agenthail owns as a git repository whose
// history records who made each change. Git runs with no user or system
// configuration and no hooks, so the person's own git setup never changes
// what is recorded.
package gitstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	Timeout    = 20 * time.Second
	lockFile   = ".lock"
	Agenthail  = "agenthail"
	UndoPrefix = "Undo "
)

// ErrConflict means a revert met a later change to the same lines and was
// abandoned, leaving the tree as it was.
var ErrConflict = errors.New("a later change edited the same lines")

type Repo struct {
	dir   string
	email string
	busy  string
}

func New(dir, email, busy string) *Repo {
	return &Repo{dir: dir, email: email, busy: busy}
}

func Available() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed on this host")
	}
	return nil
}

func (r *Repo) Dir() string { return r.dir }

func (r *Repo) Initialized() bool {
	_, err := os.Stat(filepath.Join(r.dir, ".git"))
	return err == nil
}

func (r *Repo) InitLocked() error {
	_, err := r.Git("init", "-q", "-b", "main")
	return err
}

func (r *Repo) Stage(paths ...string) error {
	args := []string{"add", "-A"}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	_, err := r.Git(args...)
	return err
}

func (r *Repo) CommitStaged(author, message string) (bool, error) {
	if _, err := r.Git("rev-parse", "-q", "--verify", "HEAD"); err == nil {
		if _, err := r.Git("diff", "--cached", "--quiet"); err == nil {
			return false, nil
		}
	}
	if _, err := r.GitAs(author, "commit", "-q", "--allow-empty", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repo) Head() (string, error) {
	rev, err := r.Git("rev-parse", "--short=12", "HEAD")
	return strings.TrimSpace(rev), err
}

type Entry struct {
	Rev     string    `json:"rev"`
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

func (r *Repo) Log(limit int, paths ...string) ([]Entry, error) {
	args := []string{"log", fmt.Sprintf("-n%d", limit), "--format=%h%x1f%an%x1f%aI%x1f%s", "--abbrev=12"}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	out, err := r.Git(args...)
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

func (r *Repo) CheckRev(rev string) error {
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("unknown revision %s", rev)
	}
	if _, err := r.Git("rev-parse", "--verify", "-q", rev+"^{commit}"); err != nil {
		return fmt.Errorf("unknown revision %s", rev)
	}
	return nil
}

// The caller holds the lock. Uncommitted edits to files rev did not touch
// survive, including an abandoned revert; the caller makes sure the files rev
// touched have none.
func (r *Repo) RevertLocked(rev, author string) (string, error) {
	if err := r.CheckRev(rev); err != nil {
		return "", err
	}
	parents, err := r.Git("rev-list", "--parents", "-n1", rev)
	if err != nil {
		return "", err
	}
	if len(strings.Fields(parents)) < 2 {
		return "", errors.New("the first commit can't be undone")
	}
	subject, err := r.Git("log", "-n1", "--format=%s", rev)
	if err != nil {
		return "", err
	}
	reverted := strings.TrimSpace(subject)
	if _, err := r.GitAs(author, "revert", "--no-edit", "--no-commit", rev); err != nil {
		_, _ = r.Git("reset", "-q", "--merge", "HEAD")
		return reverted, fmt.Errorf("can't undo %q cleanly: %w", reverted, ErrConflict)
	}
	if _, err := r.GitAs(author, "commit", "-q", "--allow-empty", "-m", UndoPrefix+reverted); err != nil {
		return reverted, err
	}
	return reverted, nil
}

func (r *Repo) Lock() (func(), error) {
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(r.dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(Timeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, errors.New(r.busy)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

func (r *Repo) Git(args ...string) (string, error) {
	return r.GitAs(Agenthail, args...)
}

func (r *Repo) GitAs(author string, args ...string) (string, error) {
	return r.GitWith(author, nil, args...)
}

func (r *Repo) GitWith(author string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	full := append([]string{"-C", r.dir, "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.autocrlf=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_AUTHOR_NAME="+author, "GIT_AUTHOR_EMAIL="+r.email,
		"GIT_COMMITTER_NAME="+author, "GIT_COMMITTER_EMAIL="+r.email,
	)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
