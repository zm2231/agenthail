package codexsurface

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func (h CodexHome) rolloutFile(sess *surface.Session) string {
	if sess.Transcript != "" {
		plain := strings.TrimSuffix(sess.Transcript, ".zst")
		for _, path := range []string{plain, plain + ".zst"} {
			if transcript.FileExists(path) {
				return path
			}
		}
	}
	var plain, compressed []string
	for _, day := range codexRolloutDays(uuidV7Time(sess.ID)) {
		found, _ := filepath.Glob(filepath.Join(h.sessionsDir(), day, "*"+sess.ID+"*.jsonl"))
		plain = append(plain, found...)
		found, _ = filepath.Glob(filepath.Join(h.sessionsDir(), day, "*"+sess.ID+"*.jsonl.zst"))
		compressed = append(compressed, found...)
	}
	for _, matches := range [][]string{plain, compressed} {
		if len(matches) > 0 {
			sort.Strings(matches)
			return matches[len(matches)-1]
		}
	}
	return sess.Transcript
}

func (h CodexHome) transcriptPath(sess *surface.Session) string {
	path, _, err := h.statTranscript(sess)
	if err != nil {
		return ""
	}
	return path
}

func (h CodexHome) statTranscript(sess *surface.Session) (string, os.FileInfo, error) {
	return statReadableRollout(h.rolloutFile(sess))
}

// A decompressed copy is touched on use and recreated when pruned, so it reports the rollout's time and identity.
func statReadableRollout(source string) (string, os.FileInfo, error) {
	if source == "" {
		return "", nil, os.ErrNotExist
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return "", nil, err
	}
	if !strings.HasSuffix(source, ".zst") {
		return source, sourceInfo, nil
	}
	path, err := codexReadableRollout(source)
	if err != nil {
		return "", nil, fmt.Errorf("read compressed Codex rollout %s: %w", filepath.Base(source), err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	return path, compressedRolloutInfo{FileInfo: info, source: sourceInfo}, nil
}

type compressedRolloutInfo struct {
	os.FileInfo
	source os.FileInfo
}

func (i compressedRolloutInfo) ModTime() time.Time { return i.source.ModTime() }
func (i compressedRolloutInfo) Sys() any           { return i.source.Sys() }

func (c *Codex) TranscriptOnDisk(sess *surface.Session) bool {
	_, _, err := c.home.statTranscript(sess)
	return err == nil
}
