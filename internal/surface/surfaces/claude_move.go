package surfaces

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

// claudeProjectDirLimit is the longest project folder name Claude writes
// verbatim. A longer name gets a hash suffix Agenthail cannot reproduce, so a
// conversation cannot be placed for a working directory that long.
const claudeProjectDirLimit = 200

// claudeMovedDir holds a moved conversation's files beside, never inside,
// the folders Claude and Agenthail read conversations from.
const claudeMovedDir = "agenthail-moved"

var claudeProjectDirUnsafe = regexp.MustCompile(`[^A-Za-z0-9]`)

func claudeProjectDirName(cwd string) string {
	return claudeProjectDirUnsafe.ReplaceAllString(cwd, "-")
}

func claudeConversationID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && !strings.HasPrefix(id, ".")
}

// claudeMoveOrigin records where a moved conversation's transcript lived, so
// a move that did not happen puts it back.
type claudeMoveOrigin struct {
	Root       string `json:"root"`
	ProjectDir string `json:"projectDir"`
}

// PrepareMove holds an idle conversation for a move: the process that holds
// it is stopped as an adoption stops it, and the conversation lock is kept
// until Release so nothing here resumes it meanwhile.
func (c *Claude) PrepareMove(ctx context.Context, sess *surface.Session, request surface.AdoptRequest) (out *surface.MoveOut, err error) {
	conversation := sess.ID
	if !claudeConversationID(conversation) {
		return nil, fmt.Errorf("Claude session %q is not a conversation id", sess.ID)
	}
	unlock, err := c.lockConversation(ctx, conversation)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			unlock()
		}
	}()
	root, err := c.conversationRoot(conversation)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, fmt.Errorf("Claude conversation %s has no transcript in any config root", conversation)
	}
	holder, err := c.adoptHolder(ctx, conversation, root)
	if err != nil {
		return nil, err
	}
	out = &surface.MoveOut{Conversation: conversation, Model: sess.ConfiguredModel}
	if kept, ok := c.hostSettings(conversation); ok {
		if kept.Model != "" {
			out.Model = kept.Model
		}
		out.PermissionMode = kept.PermissionMode
	}
	if holder != nil {
		if holder.session.Cwd != "" {
			out.Cwd = holder.session.Cwd
		}
		if out.Stopped, err = c.quiesce(ctx, conversation, root, holder, request); err != nil {
			return nil, err
		}
	}
	transcripts := c.transcripts.pass([]string{root})(conversation)
	if len(transcripts) != 1 {
		return nil, fmt.Errorf("Claude conversation %s has %d transcripts in %s", conversation, len(transcripts), root)
	}
	if out.Cwd == "" {
		out.Cwd = sess.Cwd
	}
	if out.Cwd == "" {
		out.Cwd = claudeTranscriptCwd(transcripts[0])
	}
	if out.Cwd == "" {
		return nil, fmt.Errorf("Claude conversation %s has no known working directory", conversation)
	}
	out.Title = c.conversationTitle(ctx, conversation, root)
	if out.Size, out.SHA256, err = hashFile(transcripts[0]); err != nil {
		return nil, err
	}
	projectDir := filepath.Base(filepath.Dir(transcripts[0]))
	for _, part := range claudeMoveParts(conversation)[1:] {
		size, err := regularFilesSize(claudeRootPart(root, projectDir, part))
		if err != nil {
			return nil, err
		}
		out.PartsSize += size
	}
	out.Release = unlock
	return out, nil
}

type claudeKeptSettings struct {
	PermissionMode string `json:"permissionMode"`
	Model          string `json:"model"`
}

// hostSettings is the permission mode and model the conversation last ran
// with in this machine's Claude host.
func (c *Claude) hostSettings(conversation string) (claudeKeptSettings, bool) {
	data, err := os.ReadFile(filepath.Join(claudehost.StateDir(c.home), "settings.json"))
	if err != nil {
		return claudeKeptSettings{}, false
	}
	var state struct {
		Sessions map[string]claudeKeptSettings `json:"sessions"`
	}
	if json.Unmarshal(data, &state) != nil {
		return claudeKeptSettings{}, false
	}
	kept, ok := state.Sessions[conversation]
	return kept, ok
}

// regularFilesSize is the bytes of the regular files under name, which
// WriteMoveArchive carries.
func regularFilesSize(name string) (int64, error) {
	var total int64
	err := filepath.WalkDir(name, func(_ string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func hashFile(name string) (int64, string, error) {
	file, err := os.Open(name)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	sum := sha256.New()
	size, err := io.Copy(sum, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(sum.Sum(nil)), nil
}

// claudeMoveParts are a conversation's own files, by their place in a move
// archive and under a config root: the transcript, its folder of subagent
// transcripts and tool results, and its task list.
func claudeMoveParts(conversation string) []string {
	return []string{conversation + ".jsonl", conversation, path.Join("tasks", conversation)}
}

// movedDir holds one move's copy of a conversation, so the copy a finished
// move left is never taken for another move's.
func (c *Claude) movedDir(root, conversation, moveID string) string {
	return filepath.Join(root, claudeMovedDir, conversation, moveID)
}

// rootPath is where a move archive part lives under a config root.
func claudeRootPart(root, projectDir, part string) string {
	if strings.HasPrefix(part, "tasks/") {
		return filepath.Join(root, filepath.FromSlash(part))
	}
	return filepath.Join(root, "projects", projectDir, part)
}

// ArchiveMove takes a conversation's files out of every folder Claude and
// Agenthail read conversations from, into
// <root>/agenthail-moved/<id>/<move id>/. It may run again after a crash: a
// part already archived stays where it is.
func (c *Claude) ArchiveMove(conversation, moveID string) error {
	if !claudeConversationID(conversation) || !claudeConversationID(moveID) {
		return fmt.Errorf("Claude session %q or move %q is not a valid id", conversation, moveID)
	}
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return err
	}
	transcripts := c.transcripts.pass(roots)(conversation)
	origin, archived, err := c.moveOrigin(roots, conversation, moveID)
	if err != nil {
		return err
	}
	if !archived {
		if len(transcripts) != 1 {
			return fmt.Errorf("Claude conversation %s has %d transcripts", conversation, len(transcripts))
		}
		origin = claudeMoveOrigin{Root: claudeTranscriptRoot(transcripts[0]), ProjectDir: filepath.Base(filepath.Dir(transcripts[0]))}
	}
	dir := c.movedDir(origin.Root, conversation, moveID)
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0700); err != nil {
		return err
	}
	encoded, _ := json.Marshal(origin)
	if err := writeFileAtomic(filepath.Join(dir, "origin.json"), encoded); err != nil {
		return err
	}
	for _, part := range claudeMoveParts(conversation) {
		from := claudeRootPart(origin.Root, origin.ProjectDir, part)
		if err := renameIfPresent(from, filepath.Join(dir, filepath.FromSlash(part))); err != nil {
			return err
		}
	}
	c.transcripts.pass(roots)(conversation)
	return nil
}

// RestoreMove puts a move's archived files back where ArchiveMove found
// them. A move that archived nothing leaves the conversation as it is.
func (c *Claude) RestoreMove(conversation, moveID string) error {
	if !claudeConversationID(conversation) || !claudeConversationID(moveID) {
		return fmt.Errorf("Claude session %q or move %q is not a valid id", conversation, moveID)
	}
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return err
	}
	origin, archived, err := c.moveOrigin(roots, conversation, moveID)
	if err != nil || !archived {
		return err
	}
	dir := c.movedDir(origin.Root, conversation, moveID)
	if err := os.MkdirAll(filepath.Join(origin.Root, "projects", origin.ProjectDir), 0700); err != nil {
		return err
	}
	for _, part := range claudeMoveParts(conversation) {
		to := claudeRootPart(origin.Root, origin.ProjectDir, part)
		if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
			return err
		}
		if err := renameIfPresent(filepath.Join(dir, filepath.FromSlash(part)), to); err != nil {
			return err
		}
	}
	c.transcripts.pass(roots)(conversation)
	return os.RemoveAll(dir)
}

// moveOrigin reads where a move's archived conversation came from.
func (c *Claude) moveOrigin(roots []string, conversation, moveID string) (claudeMoveOrigin, bool, error) {
	for _, root := range roots {
		data, err := os.ReadFile(filepath.Join(c.movedDir(root, conversation, moveID), "origin.json"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return claudeMoveOrigin{}, false, err
		}
		var origin claudeMoveOrigin
		if err := json.Unmarshal(data, &origin); err != nil || origin.ProjectDir == "" || origin.ProjectDir != filepath.Base(origin.ProjectDir) {
			return claudeMoveOrigin{}, false, fmt.Errorf("moved conversation %s: unreadable origin", conversation)
		}
		origin.Root = root
		return origin, true, nil
	}
	return claudeMoveOrigin{}, false, nil
}

// renameIfPresent moves from to to unless from is gone, which an earlier run
// that already moved it leaves.
func renameIfPresent(from, to string) error {
	if _, err := os.Lstat(from); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("move %s: %s already exists", from, to)
	}
	return os.Rename(from, to)
}

func writeFileAtomic(name string, data []byte) error {
	temp := name + ".tmp"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, name)
}

// WriteMoveArchive streams an archived conversation as a tar of its parts:
// <id>.jsonl, <id>/ and tasks/<id>/. Only regular files and folders are
// written.
func (c *Claude) WriteMoveArchive(w io.Writer, conversation, moveID string) error {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return err
	}
	origin, archived, err := c.moveOrigin(roots, conversation, moveID)
	if err != nil {
		return err
	}
	if !archived {
		return fmt.Errorf("Claude conversation %s is not archived for move %s", conversation, moveID)
	}
	dir := c.movedDir(origin.Root, conversation, moveID)
	archive := tar.NewWriter(w)
	for _, part := range claudeMoveParts(conversation) {
		base := filepath.Join(dir, filepath.FromSlash(part))
		if _, err := os.Lstat(base); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(base, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, name)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			switch {
			case entry.IsDir():
				return archive.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: filepath.ToSlash(rel) + "/", Mode: 0700, ModTime: info.ModTime()})
			case !entry.Type().IsRegular():
				return nil
			}
			if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: filepath.ToSlash(rel), Mode: 0600, Size: info.Size(), ModTime: info.ModTime()}); err != nil {
				return err
			}
			file, err := os.Open(name)
			if err != nil {
				return err
			}
			defer file.Close()
			written, err := io.Copy(archive, file)
			if err == nil && written != info.Size() {
				err = fmt.Errorf("%s changed while it was archived", name)
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	return archive.Close()
}

// moveInRoot is the config root a moving conversation is placed in: this
// machine's primary root.
func (c *Claude) moveInRoot() (string, []string, error) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return "", nil, err
	}
	return roots[0], roots, nil
}

// CheckMoveIn refuses a conversation this machine cannot take: a working
// directory whose project folder Claude would rename, or a copy of the
// conversation here that the incoming one does not extend.
func (c *Claude) CheckMoveIn(in surface.MoveIn) error {
	if err := checkMoveInPlace(in); err != nil {
		return err
	}
	root, roots, err := c.moveInRoot()
	if err != nil {
		return err
	}
	dest := filepath.Join(root, "projects", claudeProjectDirName(in.Cwd), in.Conversation+".jsonl")
	for _, existing := range c.transcripts.pass(roots)(in.Conversation) {
		if existing != dest {
			return fmt.Errorf("Claude conversation %s already has a transcript here at %s", in.Conversation, existing)
		}
		info, err := os.Stat(existing)
		if err != nil {
			return err
		}
		if info.Size() > in.Size {
			return fmt.Errorf("Claude conversation %s here is longer than the one arriving; the two copies have diverged", in.Conversation)
		}
	}
	return nil
}

// checkMoveInPlace refuses a conversation id or working directory Claude
// could not keep as given.
func checkMoveInPlace(in surface.MoveIn) error {
	if !claudeConversationID(in.Conversation) {
		return fmt.Errorf("Claude session %q is not a conversation id", in.Conversation)
	}
	if !filepath.IsAbs(in.Cwd) {
		return fmt.Errorf("working directory %q is not absolute", in.Cwd)
	}
	if dir := claudeProjectDirName(in.Cwd); len(dir) > claudeProjectDirLimit {
		return fmt.Errorf("working directory %s is too long for Claude to keep its project folder name (%d characters, at most %d)", in.Cwd, len(dir), claudeProjectDirLimit)
	}
	return nil
}

// ReceiveMove places an arriving conversation in this machine's primary
// config root and resumes it in the Claude host. The archive is read as
// untrusted: only the conversation's own parts, as regular files and
// folders, are accepted; they are written to a folder inside the root and
// renamed into place only once the transcript matches the size and digest
// the move declared. A conversation a process here already runs, which an
// earlier attempt of the move resumed, is only checked: what arrived must
// begin what runs here.
func (c *Claude) ReceiveMove(ctx context.Context, in surface.MoveIn, archive io.Reader, admit func() error) (*surface.Session, error) {
	if err := checkMoveInPlace(in); err != nil {
		return nil, err
	}
	if !claudeConversationID(in.MoveID) {
		return nil, fmt.Errorf("move id %q is not valid", in.MoveID)
	}
	unlock, err := c.lockConversation(ctx, in.Conversation)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := admit(); err != nil {
		return nil, err
	}
	root, roots, err := c.moveInRoot()
	if err != nil {
		return nil, err
	}
	staging := filepath.Join(root, ".agenthail-move-"+in.MoveID)
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staging, 0700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := extractMoveArchive(staging, in, archive); err != nil {
		return nil, err
	}
	if _, alive, err := c.conversationProcess(ctx, in.Conversation, claudePin{}); err != nil && !alive {
		return nil, err
	} else if alive {
		dest := filepath.Join(root, "projects", claudeProjectDirName(in.Cwd), in.Conversation+".jsonl")
		if err := checkPrefix(filepath.Join(staging, in.Conversation+".jsonl"), dest); err != nil {
			return nil, fmt.Errorf("%w with a different transcript: Claude conversation %s", surface.ErrMoveInRunning, in.Conversation)
		}
		resumed, err := c.resumeAs(ctx, in.Conversation, in.Cwd, claudehost.OpenRequest{Model: in.Model, PermissionMode: in.PermissionMode, Name: in.Title})
		if err != nil {
			return nil, fmt.Errorf("%w: %v", surface.ErrMoveInRunning, err)
		}
		return resumed, nil
	}
	if err := c.CheckMoveIn(in); err != nil {
		return nil, err
	}
	projectDir := filepath.Join(root, "projects", claudeProjectDirName(in.Cwd))
	dest := filepath.Join(projectDir, in.Conversation+".jsonl")
	if err := checkPrefix(dest, filepath.Join(staging, in.Conversation+".jsonl")); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(projectDir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0700); err != nil {
		return nil, err
	}
	resumed, err := c.placeAndResume(ctx, in, staging, root, projectDir)
	if err == nil {
		return resumed, nil
	}
	if surface.IsDeliveryOutcomeUnknown(err) {
		return nil, fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	if dropErr := c.dropMoveInLocked(ctx, in.Conversation, in.MoveID, roots); dropErr != nil {
		return nil, fmt.Errorf("%w: %v (%v)", dropErr, err, surface.ErrMoveInPlaced)
	}
	return nil, fmt.Errorf("%w: %v", surface.ErrMoveInRefused, err)
}

func (c *Claude) placeAndResume(ctx context.Context, in surface.MoveIn, staging, root, projectDir string) (*surface.Session, error) {
	for _, part := range claudeMoveParts(in.Conversation) {
		from := filepath.Join(staging, filepath.FromSlash(part))
		if _, err := os.Lstat(from); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		to := claudeRootPart(root, filepath.Base(projectDir), part)
		if err := c.setAsideExisting(root, in, part, to); err != nil {
			return nil, err
		}
		if err := os.Rename(from, to); err != nil {
			return nil, err
		}
	}
	return c.resumeAs(ctx, in.Conversation, in.Cwd, claudehost.OpenRequest{Model: in.Model, PermissionMode: in.PermissionMode, Name: in.Title})
}

// setAsideExisting moves a copy of a part the arriving conversation extends
// into the move's folder, under previous/, rather than deleting it.
func (c *Claude) setAsideExisting(root string, in surface.MoveIn, part, at string) error {
	if _, err := os.Lstat(at); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	kept := filepath.Join(c.movedDir(root, in.Conversation, in.MoveID), "previous", filepath.FromSlash(part))
	if err := os.MkdirAll(filepath.Dir(kept), 0700); err != nil {
		return err
	}
	if err := os.RemoveAll(kept); err != nil {
		return err
	}
	return os.Rename(at, kept)
}

// DropMoveIn sets aside the copy a move placed in projects/ and tasks/, into
// the move's folder under agenthail-moved/, and runs commit before any other
// Agenthail process can resume the conversation.
func (c *Claude) DropMoveIn(ctx context.Context, conversation, moveID string, admit, commit func() error) error {
	if !claudeConversationID(conversation) || !claudeConversationID(moveID) {
		return fmt.Errorf("Claude session %q or move %q is not a valid id", conversation, moveID)
	}
	unlock, err := c.lockConversation(ctx, conversation)
	if err != nil {
		return err
	}
	defer unlock()
	if err := admit(); err != nil {
		return err
	}
	_, roots, err := c.moveInRoot()
	if err != nil {
		return err
	}
	if err := c.dropMoveInLocked(ctx, conversation, moveID, roots); err != nil {
		return err
	}
	return commit()
}

// dropMoveInLocked sets aside a move's placed copy unless a process here
// holds the conversation. The caller holds the conversation lock.
func (c *Claude) dropMoveInLocked(ctx context.Context, conversation, moveID string, roots []string) error {
	if _, alive, err := c.conversationProcess(ctx, conversation, claudePin{}); alive {
		return surface.ErrMoveInRunning
	} else if err != nil {
		return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	placed := c.transcripts.pass(roots)(conversation)
	_, archived, err := c.moveOrigin(roots, conversation, moveID)
	if err != nil {
		return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	if len(placed) == 0 && !archived {
		return nil
	}
	if archived && len(placed) == 1 {
		// An earlier attempt of the move already set aside the files it
		// declared, so this attempt's copy of them is removed.
		root, projectDir := claudeTranscriptRoot(placed[0]), filepath.Base(filepath.Dir(placed[0]))
		for _, part := range claudeMoveParts(conversation) {
			if err := os.RemoveAll(claudeRootPart(root, projectDir, part)); err != nil {
				return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
			}
		}
		c.transcripts.pass(roots)(conversation)
		return nil
	}
	if err := c.ArchiveMove(conversation, moveID); err != nil {
		return fmt.Errorf("%w: %v", surface.ErrMoveInPlaced, err)
	}
	return nil
}

// moveArchivePart reports whether name, a cleaned slash path, is one of the
// conversation's parts or lies inside one.
func moveArchivePart(conversation, name string, dir bool) bool {
	switch {
	case name == conversation+".jsonl":
		return !dir
	case name == conversation || name == "tasks" || name == "tasks/"+conversation:
		return dir
	}
	return strings.HasPrefix(name, conversation+"/") || strings.HasPrefix(name, "tasks/"+conversation+"/")
}

func extractMoveArchive(staging string, in surface.MoveIn, archive io.Reader) error {
	reader := tar.NewReader(archive)
	transcript := false
	parts := int64(0)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read the moved conversation: %w", err)
		}
		raw := strings.TrimSuffix(header.Name, "/")
		name := path.Clean(raw)
		if raw == "" || name != raw || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
			return fmt.Errorf("moved conversation names an unsafe path %q", header.Name)
		}
		dir := header.Typeflag == tar.TypeDir
		if !dir && header.Typeflag != tar.TypeReg {
			return fmt.Errorf("moved conversation holds %q, which is not a regular file or folder", header.Name)
		}
		if !moveArchivePart(in.Conversation, name, dir) {
			return fmt.Errorf("moved conversation holds %q, which is not part of conversation %s", header.Name, in.Conversation)
		}
		target := filepath.Join(staging, filepath.FromSlash(name))
		if dir {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		var sink io.Writer = file
		sum := sha256.New()
		if name == in.Conversation+".jsonl" {
			if header.Size != in.Size {
				_ = file.Close()
				return fmt.Errorf("the moved transcript is %d bytes, not the %d the move declared", header.Size, in.Size)
			}
			transcript = true
			sink = io.MultiWriter(file, sum)
		} else if parts += header.Size; header.Size < 0 || parts > in.PartsSize {
			_ = file.Close()
			return fmt.Errorf("the moved conversation's other files are more than the %d bytes the move declared", in.PartsSize)
		}
		written, copyErr := io.Copy(sink, reader)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("read the moved conversation: %w", copyErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if name == in.Conversation+".jsonl" && (written != in.Size || hex.EncodeToString(sum.Sum(nil)) != in.SHA256) {
			return fmt.Errorf("moved transcript of %s is %d bytes with digest %s, not the %d bytes and digest %s the move declared", in.Conversation, written, hex.EncodeToString(sum.Sum(nil)), in.Size, in.SHA256)
		}
	}
	if !transcript {
		return fmt.Errorf("moved conversation %s has no transcript", in.Conversation)
	}
	return nil
}

// checkPrefix refuses unless the file at shorter is absent or begins the file
// at longer, so a move never drops turns either copy has.
func checkPrefix(shorter, longer string) error {
	existing, err := os.Open(shorter)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer existing.Close()
	arriving, err := os.Open(longer)
	if err != nil {
		return err
	}
	defer arriving.Close()
	left := make([]byte, 64<<10)
	right := make([]byte, 64<<10)
	for {
		n, readErr := io.ReadFull(existing, left)
		if n > 0 {
			if _, err := io.ReadFull(arriving, right[:n]); err != nil || string(left[:n]) != string(right[:n]) {
				return fmt.Errorf("the transcript here and the one arriving have diverged")
			}
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
