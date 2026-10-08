package surfaces

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
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

func (c *Codex) ValidateTranscriptContinuation(previous, current surface.Session) bool {
	if previous.ID != current.ID || previous.Transcript == current.Transcript || current.TranscriptIdentity == "" {
		return false
	}
	if !codexRolloutNamesSession(previous.Transcript, previous.ID) || !codexRolloutNamesSession(current.Transcript, current.ID) {
		return false
	}
	old, err := os.Stat(previous.Transcript)
	if err != nil || previous.TranscriptIdentity != "" && transcriptFileIdentity(old) != previous.TranscriptIdentity {
		return false
	}
	info, err := os.Stat(current.Transcript)
	return err == nil && transcriptFileIdentity(info) == current.TranscriptIdentity && indexedCodexRollout(codexHome(), current.ID) == current.Transcript
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
