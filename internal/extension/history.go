package extension

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/gitstore"
)

const (
	Person         = "person"
	handEdit       = "Edited by hand"
	sessionTrailer = "Agenthail-Session"
	turnTrailer    = "Agenthail-Turn"
	alsoTrailer    = "Agenthail-Also-Running"
	gitignore      = "node_modules/\n.DS_Store\n.lock\n"
)

var (
	ErrUndoLater  = errors.New("undo the later change first")
	ErrUnrecorded = errors.New("undo it once the turn making those changes ends")
)

// The workspace is a git repository the daemon owns, so every change an
// agent's turn makes is a commit that can be shown and undone.
type History struct {
	repo *gitstore.Repo
}

type Change struct {
	Rev       string    `json:"rev"`
	Author    string    `json:"author"`
	Time      time.Time `json:"time"`
	Message   string    `json:"message"`
	SessionID string    `json:"sessionId,omitempty"`
	TurnID    string    `json:"turnId,omitempty"`
	// AlsoRunning lists sessions that were mid-turn when the change was
	// recorded; the change may hold their edits too.
	AlsoRunning []string      `json:"alsoRunning,omitempty"`
	Extensions  []FileChanges `json:"extensions,omitempty"`
	// Root is the history's first commit, which can't be undone.
	Root bool `json:"root,omitempty"`
}

type FileChanges struct {
	ID      string   `json:"id"`
	Created bool     `json:"created,omitempty"`
	Removed bool     `json:"removed,omitempty"`
	Added   []string `json:"added,omitempty"`
	Updated []string `json:"updated,omitempty"`
	Deleted []string `json:"deleted,omitempty"`
}

// Opened from a turn's end, existing is false so files the turn created land
// in the turn's commit rather than in the starting commit.
func OpenHistory(home string, existing bool) (*History, error) {
	if err := gitstore.Available(); err != nil {
		return nil, errors.New("extension history needs git on this host")
	}
	dir := Workspace(home)
	h := &History{repo: gitstore.New(dir, "extensions@agenthail.invalid", "the extensions folder is busy: another change is still being saved")}
	if h.repo.Initialized() {
		return h, nil
	}
	unlock, err := h.repo.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := h.repo.InitLocked(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o600); err != nil {
		return nil, err
	}
	paths := []string{}
	if !existing {
		paths = append(paths, ".gitignore")
	}
	if err := h.repo.Stage(paths...); err != nil {
		return nil, err
	}
	if _, err := h.repo.CommitStaged("agenthail", "Start the extensions history"); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *History) commitLocked(author, message string) (bool, error) {
	if err := h.repo.Stage(); err != nil {
		return false, err
	}
	return h.repo.CommitStaged(author, message)
}

func (h *History) changeAt(rev string) (Change, error) {
	entries, err := h.Log(1, rev)
	if err != nil {
		return Change{}, err
	}
	if len(entries) == 0 {
		return Change{}, fmt.Errorf("unknown revision %s", rev)
	}
	return entries[0], nil
}

// CommitHandEdit records whatever changed in the folder outside an agent's
// turn as the person's, so the next turn's change holds only that turn.
func (h *History) CommitHandEdit() (Change, bool, error) {
	return h.commit(Person, handEdit)
}

func (h *History) CommitTurn(sessionID, author, turnID string, also []string) (Change, bool, error) {
	message := fmt.Sprintf("Turn in %s\n\n%s: %s\n%s: %s", author, sessionTrailer, sessionID, turnTrailer, turnID)
	for _, id := range also {
		message += fmt.Sprintf("\n%s: %s", alsoTrailer, id)
	}
	return h.commit(author, message)
}

func (h *History) commit(author, message string) (Change, bool, error) {
	unlock, err := h.repo.Lock()
	if err != nil {
		return Change{}, false, err
	}
	defer unlock()
	changed, err := h.commitLocked(author, message)
	if err != nil || !changed {
		return Change{}, false, err
	}
	change, err := h.changeAt("HEAD")
	return change, true, err
}

// Log lists up to limit changes, newest first, starting at rev. With ids it
// keeps changes touching those extensions but lists all their files, since
// undo reverts the whole change.
func (h *History) Log(limit int, rev string, ids ...string) ([]Change, error) {
	if err := h.repo.CheckRev(rev); err != nil {
		return nil, err
	}
	args := []string{"log", fmt.Sprintf("-n%d", limit), "--format=%x1e%h%x1f%an%x1f%aI%x1f%s%x1f%(trailers:key=" + sessionTrailer + ",valueonly,separator=)%x1f%(trailers:key=" + turnTrailer + ",valueonly,separator=)%x1f%(trailers:key=" + alsoTrailer + ",valueonly,separator=%x2C)%x1f%P", "--abbrev=12", "--name-status", "--no-renames", "--full-diff", rev, "--"}
	for _, id := range ids {
		if !ValidID(id) {
			return nil, fmt.Errorf("invalid extension id %q", id)
		}
		args = append(args, id+"/")
	}
	out, err := h.repo.Git(args...)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, record := range strings.Split(out, "\x1e") {
		header, files, _ := strings.Cut(record, "\n")
		fields := strings.Split(header, "\x1f")
		if len(fields) != 8 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, fields[2])
		change := Change{Rev: fields[0], Author: fields[1], Time: at, Message: fields[3], SessionID: strings.TrimSpace(fields[4]), TurnID: strings.TrimSpace(fields[5])}
		if also := strings.TrimSpace(fields[6]); also != "" {
			change.AlsoRunning = strings.Split(also, ",")
		}
		change.Root = strings.TrimSpace(fields[7]) == ""
		change.Extensions = fileChanges(files)
		changes = append(changes, change)
	}
	return changes, nil
}

func fileChanges(lines string) []FileChanges {
	byID := map[string]*FileChanges{}
	var order []string
	for _, line := range strings.Split(lines, "\n") {
		status, path, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		id, rel, ok := strings.Cut(path, "/")
		if !ok || !ValidID(id) {
			continue
		}
		files := byID[id]
		if files == nil {
			files = &FileChanges{ID: id}
			byID[id] = files
			order = append(order, id)
		}
		switch status {
		case "A":
			files.Added = append(files.Added, rel)
			files.Created = files.Created || rel == ManifestFile
		case "D":
			files.Deleted = append(files.Deleted, rel)
			files.Removed = files.Removed || rel == ManifestFile
		default:
			files.Updated = append(files.Updated, rel)
		}
	}
	out := make([]FileChanges, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// Patch is what rev changed, as git shows it, cut at limit bytes.
func (h *History) Patch(rev string, limit int) (string, bool, error) {
	if err := h.repo.CheckRev(rev); err != nil {
		return "", false, err
	}
	out, err := h.repo.Git("show", "--format=", "--patch", "--no-color", "--no-renames", "--no-ext-diff", rev)
	if err != nil {
		return "", false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// FolderTrees hashes each extension's folder as it is on disk, the way a
// commit would record it, through a scratch index so nothing is staged.
func (h *History) FolderTrees() (map[string]string, error) {
	index, err := os.CreateTemp(filepath.Join(h.repo.Dir(), ".git"), "verify-index-")
	if err != nil {
		return nil, err
	}
	name := index.Name()
	index.Close()
	os.Remove(name)
	defer os.Remove(name)
	env := []string{"GIT_INDEX_FILE=" + name}
	if _, err := h.repo.GitWith("agenthail", env, "add", "-A"); err != nil {
		return nil, err
	}
	tree, err := h.repo.GitWith("agenthail", env, "write-tree")
	if err != nil {
		return nil, err
	}
	out, err := h.repo.Git("ls-tree", strings.TrimSpace(tree))
	if err != nil {
		return nil, err
	}
	trees := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		meta, id, ok := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if ok && len(fields) == 3 && fields[1] == "tree" && ValidID(id) {
			trees[id] = fields[2]
		}
	}
	return trees, nil
}

// CommittedTree is the hash of an extension's folder as rev recorded it, or
// "" when rev holds no such folder.
func (h *History) CommittedTree(rev, id string) (string, error) {
	if err := h.repo.CheckRev(rev); err != nil {
		return "", err
	}
	return h.subtree(rev, id)
}

func (h *History) subtree(treeish, id string) (string, error) {
	out, err := h.repo.Git("ls-tree", treeish, "--", id)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[1] != "tree" {
		return "", nil
	}
	return fields[2], nil
}

// Undone reports what an undo did: the change it reverted, and the
// extensions that change had created, which no longer exist.
type Undone struct {
	Reverted Change
	Removed  []string
}

// Undo reverts rev as the person's change. Undoing the change that created an
// extension waits until every later change to that extension is undone, and
// then removes what is left of its folder, such as installed packages.
// Uncommitted edits may belong to a turn still running, so they are never
// recorded or discarded here: an undo that would touch them is refused.
func (h *History) Undo(rev string) (Undone, error) {
	unlock, err := h.repo.Lock()
	if err != nil {
		return Undone{}, err
	}
	defer unlock()
	change, err := h.changeAt(rev)
	if err != nil {
		return Undone{}, err
	}
	paths, err := h.repo.Git("diff-tree", "--no-commit-id", "--name-only", "-r", "--root", change.Rev)
	if err != nil {
		return Undone{}, err
	}
	touched := strings.Fields(paths)
	for _, files := range change.Extensions {
		if files.Created {
			touched = append(touched, files.ID)
		}
	}
	if len(touched) > 0 {
		dirty, err := h.repo.Git(append([]string{"status", "--porcelain", "--untracked-files=all", "--"}, touched...)...)
		if err != nil {
			return Undone{}, err
		}
		if strings.TrimSpace(dirty) != "" {
			return Undone{}, fmt.Errorf("files it touched have changes not recorded yet: %w", ErrUnrecorded)
		}
	}
	var created []string
	for _, files := range change.Extensions {
		if !files.Created {
			continue
		}
		later, err := h.repo.Git("rev-list", "-n1", change.Rev+"..HEAD", "--", files.ID)
		if err != nil {
			return Undone{}, err
		}
		if strings.TrimSpace(later) != "" {
			return Undone{}, fmt.Errorf("%s was changed after it was created: %w", files.ID, ErrUndoLater)
		}
		created = append(created, files.ID)
	}
	if _, err := h.repo.RevertLocked(change.Rev, Person); err != nil {
		if errors.Is(err, gitstore.ErrConflict) {
			return Undone{}, fmt.Errorf("%w: %w", err, ErrUndoLater)
		}
		return Undone{}, err
	}
	for _, id := range created {
		if err := os.RemoveAll(filepath.Join(h.repo.Dir(), id)); err != nil {
			return Undone{}, err
		}
	}
	return Undone{Reverted: change, Removed: slices.Clip(created)}, nil
}
