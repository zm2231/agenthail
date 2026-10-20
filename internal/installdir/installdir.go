// Package installdir finds the files Agenthail installs beside its executable.
// On macOS the package puts the daemon executable inside a bundle, so macOS
// attributes the daemon's privacy grants to that bundle, and the rest of the
// installation sits in the directory that holds the bundle.
package installdir

import (
	"path/filepath"
	"strings"
)

// Of returns the directory holding Agenthail's installed files for the
// executable at exe: the directory around the bundle when exe is a bundle's
// main executable, otherwise exe's own directory.
func Of(exe string) string {
	if bundle := Bundle(exe); bundle != "" {
		return filepath.Dir(bundle)
	}
	return filepath.Dir(exe)
}

// Bundle returns the .app bundle whose main executable is exe, or "".
func Bundle(exe string) string {
	macOS := filepath.Dir(exe)
	contents := filepath.Dir(macOS)
	bundle := filepath.Dir(contents)
	if filepath.Base(macOS) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
		return ""
	}
	return bundle
}
