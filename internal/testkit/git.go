package testkit

import (
	"os"
	"path/filepath"
)

// StopGitAtTempDir keeps git from looking above the temporary directory for a
// repository, so a test's folder is never taken for part of a checkout the
// temporary directory happens to sit in.
func StopGitAtTempDir() {
	ceilings := filepath.Clean(os.TempDir())
	if existing := os.Getenv("GIT_CEILING_DIRECTORIES"); existing != "" {
		ceilings += string(os.PathListSeparator) + existing
	}
	os.Setenv("GIT_CEILING_DIRECTORIES", ceilings)
}
