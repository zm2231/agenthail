package surfaces

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func (c *Codex) ValidateTranscriptContinuation(ctx context.Context, previous, current surface.Session) bool {
	if ctx.Err() != nil {
		return false
	}
	if previous.ID != current.ID || current.TranscriptIdentity == "" {
		return false
	}
	if checkpoint, ok := decodeCodexCheckpoint(previous.TranscriptIdentity); ok {
		previous.Transcript = checkpoint.Path
	} else if previous.TranscriptIdentity != "" {
		info, err := os.Stat(previous.Transcript)
		if err != nil || !sameTranscriptInode(transcriptFileIdentity(info), previous.TranscriptIdentity) {
			path := recoverCodexCheckpoint(ctx, previous.ID, previous.TranscriptIdentity, current.Transcript)
			if path == "" {
				return false
			}
			previous.Transcript = path
		}
	}
	if !codexRolloutNamesSession(previous.Transcript, previous.ID) || !codexRolloutNamesSession(current.Transcript, current.ID) {
		return false
	}
	old, err := os.Stat(previous.Transcript)
	if err != nil {
		return false
	}
	if _, ok := decodeCodexCheckpoint(previous.TranscriptIdentity); ok && codexTranscriptIdentity(previous.Transcript, old) != previous.TranscriptIdentity {
		return false
	}
	if previous.Transcript == current.Transcript {
		if _, ok := decodeCodexCheckpoint(previous.TranscriptIdentity); ok || previous.TranscriptIdentity == "" {
			return false
		}
	}
	info, err := os.Stat(current.Transcript)
	return err == nil && codexTranscriptIdentity(current.Transcript, info) == current.TranscriptIdentity && indexedCodexRollout(codexHome(), current.ID) == current.Transcript
}

func sameTranscriptInode(left, right string) bool {
	_, a, aOK := strings.Cut(left, ":")
	_, b, bOK := strings.Cut(right, ":")
	return aOK && bOK && a != "" && a == b
}

type codexTranscriptCheckpoint struct {
	Path   string `json:"path"`
	Inode  string `json:"inode"`
	Header string `json:"header"`
}

func codexTranscriptIdentity(path string, info os.FileInfo) string {
	_, inode, ok := strings.Cut(transcriptFileIdentity(info), ":")
	if !ok {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return ""
	}
	line, err := bufio.NewReaderSize(file, 64<<10).ReadSlice('\n')
	if err != nil && err != io.EOF {
		return ""
	}
	data, err := json.Marshal(codexTranscriptCheckpoint{Path: path, Inode: inode, Header: fmt.Sprintf("%x", sha256.Sum256(line))})
	if err != nil {
		return ""
	}
	return "codex:1:" + base64.RawURLEncoding.EncodeToString(data)
}

func decodeCodexCheckpoint(identity string) (codexTranscriptCheckpoint, bool) {
	encoded, ok := strings.CutPrefix(identity, "codex:1:")
	var checkpoint codexTranscriptCheckpoint
	if !ok || len(encoded) > 16<<10 {
		return checkpoint, false
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(data, &checkpoint) != nil || !filepath.IsAbs(checkpoint.Path) || checkpoint.Inode == "" || checkpoint.Header == "" {
		return checkpoint, false
	}
	return checkpoint, true
}

// Legacy checkpoints identify an inode but omit the path. A paginated rollout's
// history boundary verifies which earlier file belongs to its inherited history.
func recoverCodexCheckpoint(ctx context.Context, sessionID, identity, currentPath string) string {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_, inode, ok := strings.Cut(identity, ":")
	if !ok || inode == "" || strings.HasPrefix(identity, "codex:") {
		return ""
	}
	file, err := os.Open(currentPath)
	if err != nil {
		return ""
	}
	line, err := bufio.NewReaderSize(file, 64<<10).ReadSlice('\n')
	file.Close()
	var meta struct {
		Payload struct {
			ID   string `json:"id"`
			Base *struct {
				ThreadID string `json:"thread_id"`
				End      int64  `json:"end_byte_offset"`
				Ordinal  int64  `json:"end_ordinal_exclusive"`
			} `json:"history_base"`
		} `json:"payload"`
	}
	if err != nil || json.Unmarshal(line, &meta) != nil || meta.Payload.ID != sessionID || meta.Payload.Base == nil || meta.Payload.Base.ThreadID != sessionID || meta.Payload.Base.End <= 0 || meta.Payload.Base.Ordinal <= 0 {
		return ""
	}
	var matched string
	err = walkCodexCheckpoints(ctx, filepath.Join(codexHome(), "sessions"), 100000, func(path string, entry os.DirEntry) error {
		if entry.IsDir() || filepath.Ext(path) != ".jsonl" || !strings.Contains(entry.Name(), sessionID) || path == currentPath {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		_, candidateInode, _ := strings.Cut(transcriptFileIdentity(info), ":")
		if candidateInode != inode || !codexRolloutNamesSession(path, sessionID) || info.Size() < meta.Payload.Base.End {
			return nil
		}
		old, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer old.Close()
		end := meta.Payload.Base.End
		start := max(int64(0), end-(64<<10))
		data := make([]byte, end-start)
		if _, err := old.ReadAt(data, start); err != nil || len(data) == 0 || data[len(data)-1] != '\n' {
			return nil
		}
		data = data[:len(data)-1]
		if index := strings.LastIndexByte(string(data), '\n'); index >= 0 {
			data = data[index+1:]
		}
		var boundary struct {
			Ordinal *int64 `json:"ordinal"`
		}
		if json.Unmarshal(data, &boundary) != nil || boundary.Ordinal == nil || *boundary.Ordinal+1 != meta.Payload.Base.Ordinal {
			return nil
		}
		if matched != "" {
			matched = ""
			return fmt.Errorf("ambiguous checkpoint source")
		}
		matched = path
		return nil
	})
	if err != nil {
		return ""
	}
	return matched
}

func walkCodexCheckpoints(ctx context.Context, root string, limit int, visit func(string, os.DirEntry) error) error {
	pending := []string{root}
	count := 0
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		file, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = func() error {
			defer file.Close()
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				entries, readErr := file.ReadDir(256)
				for _, entry := range entries {
					count++
					if count > limit {
						return fmt.Errorf("checkpoint recovery entry limit exceeded")
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					path := filepath.Join(dir, entry.Name())
					if entry.IsDir() {
						pending = append(pending, path)
					}
					if err := visit(path, entry); err != nil {
						return err
					}
				}
				if readErr == io.EOF {
					return nil
				}
				if readErr != nil {
					return readErr
				}
			}
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// The thread index follows rollout continuations across dates and compactions.
func indexedCodexRollout(home, sessionID string) string {
	files, _ := filepath.Glob(filepath.Join(home, "state_*.sqlite"))
	sort.Slice(files, func(i, j int) bool { return codexStateVersion(files[i]) > codexStateVersion(files[j]) })
	if len(files) == 0 {
		return ""
	}
	u := url.URL{Scheme: "file", Path: files[0], RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return ""
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var path string
	if db.QueryRowContext(ctx, "SELECT rollout_path FROM threads WHERE id=?", sessionID).Scan(&path) != nil || !filepath.IsAbs(path) {
		return ""
	}
	if !codexRolloutNamesSession(path, sessionID) {
		return ""
	}
	return path
}

func codexStateVersion(path string) int {
	value := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "state_"), ".sqlite")
	version, _ := strconv.Atoi(value)
	return version
}

func codexRolloutNamesSession(path, sessionID string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	line, err := bufio.NewReaderSize(file, 64<<10).ReadSlice('\n')
	if err != nil {
		return false
	}
	var meta codexSessionMetaLine
	return json.Unmarshal(line, &meta) == nil && meta.Type == "session_meta" && meta.Payload.ID == sessionID
}
