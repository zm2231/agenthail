// Package buildinfo names the build an agenthail binary came from, so the CLI,
// the daemon it talks to and the apps can tell whether they are the same build.
package buildinfo

import (
	"runtime/debug"
)

const (
	devVersion      = "dev"
	unknownRevision = "unknown"
)

type Identity struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	Modified bool   `json:"modified"`
	BuiltAt  string `json:"builtAt,omitempty"`
}

// Resolve prefers what the release build stamped with -ldflags and fills the
// rest from the module and VCS data the Go toolchain embeds.
func Resolve(version, revision, builtAt string) Identity {
	info, _ := debug.ReadBuildInfo()
	return resolve(version, revision, builtAt, info)
}

func resolve(version, revision, builtAt string, info *debug.BuildInfo) Identity {
	identity := Identity{Version: version, Revision: revision, BuiltAt: builtAt}
	if identity.Version == "" {
		identity.Version = devVersion
	}
	if identity.Revision == "" {
		identity.Revision = unknownRevision
	}
	if info == nil {
		return identity
	}
	if identity.Version == devVersion && info.Main.Version != "" && info.Main.Version != "(devel)" {
		identity.Version = info.Main.Version
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if identity.Revision == unknownRevision {
				identity.Revision = setting.Value
			}
		case "vcs.time":
			if identity.BuiltAt == "" {
				identity.BuiltAt = setting.Value
			}
		case "vcs.modified":
			identity.Modified = setting.Value == "true"
		}
	}
	return identity
}

// RevisionKnown reports whether the build names the commit it came from.
func (i Identity) RevisionKnown() bool {
	return i.Revision != "" && i.Revision != unknownRevision
}

// SameBuild reports whether two builds come from one commit, and whether that
// can be decided: a build from a modified tree is not fully named by its
// commit, so it is never called the same as or different from another.
func SameBuild(a, b Identity) (same, known bool) {
	if !a.RevisionKnown() || !b.RevisionKnown() || a.Modified || b.Modified {
		return false, false
	}
	return a.Revision == b.Revision, true
}

// String names the build as people read it: v0.2.68 (e387b0d8).
func (i Identity) String() string {
	revision := i.Revision
	if len(revision) > 8 {
		revision = revision[:8]
	}
	if i.Modified {
		revision += ", modified"
	}
	return i.Version + " (" + revision + ")"
}
