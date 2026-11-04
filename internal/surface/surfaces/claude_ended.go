package surfaces

import (
	"os"
	"path/filepath"
	"time"
)

// An unmounted config root says nothing about its transcripts.
func (c *Claude) EndedConversation(transcript string) (bool, time.Time) {
	if transcript == "" {
		return false, time.Time{}
	}
	info, err := os.Stat(transcript)
	if err == nil {
		return true, info.ModTime()
	}
	if !os.IsNotExist(err) {
		return true, time.Time{}
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(transcript))); err != nil {
		return true, time.Time{}
	}
	return false, time.Time{}
}
