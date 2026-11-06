package worktree

import "strings"

// regenerableDirs hold what a build or package manager makes again, so
// ignored files inside them never keep a worktree.
var regenerableDirs = map[string]bool{
	"node_modules": true, ".build": true, "build": true, "dist": true, "target": true,
	"bin": true, "obj": true, "DerivedData": true, "__pycache__": true, ".venv": true,
	".gradle": true, ".next": true, "coverage": true,
}

// Regenerable reports whether a worktree-relative path is inside one of the
// regenerable folders, at any depth.
func Regenerable(path string) bool {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		if regenerableDirs[part] {
			return true
		}
	}
	return strings.HasSuffix(path, "/") && regenerableDirs[parts[len(parts)-1]]
}
