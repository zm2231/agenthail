package testkit

import (
	"os"
	"path/filepath"
	"strconv"
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

// stopGitAutoMaintenance keeps the git commands a test runs from leaving
// maintenance running in the background, which writes lock files under a
// fixture's .git after the command returns and can outlive the test.
func stopGitAutoMaintenance() {
	count, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	for _, entry := range [][2]string{{"maintenance.auto", "false"}, {"gc.auto", "0"}} {
		os.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(count), entry[0])
		os.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(count), entry[1])
		count++
	}
	os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(count))
}
