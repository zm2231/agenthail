package surface

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const worktreesDir = ".worktrees"

func FolderGone(dir string) bool {
	info, err := os.Stat(dir)
	return os.IsNotExist(err) || err == nil && !info.IsDir()
}

// FolderBounds names the folders a session may work in; none is unbounded.
type FolderBounds func(sessionID string) ([]string, error)

type FolderMove struct {
	Gone     string
	Into     string
	Checkout bool
}

func (m FolderMove) Line() string {
	if m.Checkout {
		return fmt.Sprintf("[agenthail] Your folder %s was removed; you are now in %s, a shared checkout. Don't edit or commit here; make a new worktree under %s if you need to change code.", m.Gone, m.Into, filepath.Join(m.Into, worktreesDir))
	}
	return fmt.Sprintf("[agenthail] Your folder %s was removed; you are now in %s, the nearest folder that remains. Check what it holds before you change anything here.", m.Gone, m.Into)
}

func (m FolderMove) Notice() string {
	return fmt.Sprintf("Its folder %s is gone, so it resumed in %s.", m.Gone, m.Into)
}

// FallbackFolder is where a conversation whose folder gone was removed
// resumes: the nearest folder above it that remains, taken past a .worktrees
// folder to the checkout that holds it. It refuses the root, the home folder
// or anything above it, and a folder outside roots, the folders the session
// may work in; no roots is unbounded. A root inside a worktree counts as the
// checkout the worktree was made from, as worktreeCeiling narrowed it from.
func FallbackFolder(gone, home string, roots []string) (FolderMove, error) {
	gone = filepath.Clean(gone)
	into := filepath.Dir(gone)
	for into != filepath.Dir(into) && FolderGone(into) {
		into = filepath.Dir(into)
	}
	checkout := false
	if filepath.Base(into) == worktreesDir {
		into, checkout = filepath.Dir(into), true
	}
	refused := func(why string) (FolderMove, error) {
		return FolderMove{}, fmt.Errorf("its folder %s is gone and %s; recreate %s, or archive the session", gone, why, gone)
	}
	if into == filepath.Dir(into) || home != "" && within(filepath.Clean(home), into) {
		return refused(fmt.Sprintf("the nearest folder that remains is %s", into))
	}
	if len(roots) > 0 && !withinAny(into, checkoutRoots(roots)) {
		return refused(fmt.Sprintf("the nearest folder that remains, %s, is outside the folders it may work in", into))
	}
	return FolderMove{Gone: gone, Into: into, Checkout: checkout}, nil
}

func checkoutRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = filepath.Clean(root)
		parts := strings.Split(root, string(filepath.Separator))
		for i := len(parts) - 2; i > 0; i-- {
			if parts[i] == worktreesDir {
				root = strings.Join(parts[:i], string(filepath.Separator))
				break
			}
		}
		out = append(out, root)
	}
	return out
}

func withinAny(path string, roots []string) bool {
	for _, root := range roots {
		if within(path, root) {
			return true
		}
	}
	return false
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}
