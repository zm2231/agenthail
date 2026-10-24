package daemon

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

const (
	sessionFilesListDeadline = 2 * time.Second
	sessionFilesFresh        = 10 * time.Second
	sessionFilesDefaultLimit = 50
	sessionFilesMaxLimit     = 200
	sessionFilesCachedLists  = 8
)

var sessionFilesMaxEntries = 50000

var initFileScoring = sync.OnceFunc(func() { algo.Init("path") })

type sessionFile struct {
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
}

type sessionFileList struct {
	files     []sessionFile
	truncated bool
	built     time.Time
	indexPath string
	index     time.Time
}

// Only the most recently used checkouts are kept, and concurrent requests for
// one checkout share a single listing.
type sessionFileLists struct {
	mu       sync.Mutex
	lists    map[string]*sessionFileList
	recent   []string
	building map[string]chan struct{}
}

func (l *sessionFileLists) get(ctx context.Context, cwd string, now time.Time) *sessionFileList {
	for {
		l.mu.Lock()
		cached := l.lists[cwd]
		if cached != nil && now.Sub(cached.built) < sessionFilesFresh && modTime(cached.indexPath).Equal(cached.index) {
			l.touch(cwd)
			l.mu.Unlock()
			return cached
		}
		if wait, ok := l.building[cwd]; ok {
			l.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return &sessionFileList{truncated: true}
			}
		}
		if l.building == nil {
			l.building = map[string]chan struct{}{}
		}
		done := make(chan struct{})
		l.building[cwd] = done
		l.mu.Unlock()

		return l.build(ctx, cwd, now, done)
	}
}

// The listing is shared with every request waiting on it, so one request
// going away must not cut it short; its own deadline still bounds it.
func (l *sessionFileLists) build(ctx context.Context, cwd string, now time.Time, done chan struct{}) *sessionFileList {
	var list *sessionFileList
	defer func() {
		l.mu.Lock()
		delete(l.building, cwd)
		close(done)
		if list != nil {
			if l.lists == nil {
				l.lists = map[string]*sessionFileList{}
			}
			l.lists[cwd] = list
			l.touch(cwd)
			for len(l.recent) > sessionFilesCachedLists {
				delete(l.lists, l.recent[0])
				l.recent = l.recent[1:]
			}
		}
		l.mu.Unlock()
	}()
	list = buildFileList(context.WithoutCancel(ctx), cwd, now)
	return list
}

func (l *sessionFileLists) touch(cwd string) {
	for i, recent := range l.recent {
		if recent == cwd {
			l.recent = append(l.recent[:i], l.recent[i+1:]...)
			break
		}
	}
	l.recent = append(l.recent, cwd)
}

var buildFileList = buildSessionFileList

func buildSessionFileList(ctx context.Context, cwd string, now time.Time) *sessionFileList {
	ctx, cancel := context.WithTimeout(ctx, sessionFilesListDeadline)
	defer cancel()
	indexPath := gitIndexPath(ctx, cwd)
	index := modTime(indexPath)
	list, ok := listGitFiles(ctx, cwd)
	if !ok {
		list = walkSessionFiles(ctx, cwd)
	}
	list.built, list.indexPath, list.index = now, indexPath, index
	return list
}

func gitIndexPath(ctx context.Context, cwd string) string {
	out, err := gitCommand(ctx, cwd, "rev-parse", "--git-path", "index").Output()
	if err != nil {
		return ""
	}
	index := strings.TrimSpace(string(out))
	if !filepath.IsAbs(index) {
		index = filepath.Join(cwd, index)
	}
	return index
}

func modTime(name string) time.Time {
	if name == "" {
		return time.Time{}
	}
	info, err := os.Stat(name)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func listGitFiles(ctx context.Context, cwd string) (*sessionFileList, bool) {
	if err := gitCommand(ctx, cwd, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return nil, false
	}
	cmd := gitCommand(ctx, cwd, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false
	}
	list := &sessionFileList{}
	dirs := map[string]bool{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	scanner.Split(splitNUL)
	for scanner.Scan() {
		name := scanner.Text()
		if name == "" || seen[name] {
			continue
		}
		if len(list.files)+len(dirs) >= sessionFilesMaxEntries {
			list.truncated = true
			break
		}
		seen[name] = true
		list.files = append(list.files, sessionFile{Path: name})
		for dir := path.Dir(name); dir != "." && !dirs[dir]; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	_ = cmd.Process.Kill()
	waitErr := cmd.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		list.truncated = true
	} else if waitErr != nil && !list.truncated {
		return nil, false
	}
	for dir := range dirs {
		list.files = append(list.files, sessionFile{Path: dir, Dir: true})
	}
	if len(list.files) > sessionFilesMaxEntries {
		list.files, list.truncated = list.files[:sessionFilesMaxEntries], true
	}
	return list, true
}

func splitNUL(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// WalkDir never follows a symlinked directory, so the walk stays inside cwd.
func walkSessionFiles(ctx context.Context, cwd string) *sessionFileList {
	list := &sessionFileList{}
	_ = filepath.WalkDir(cwd, func(full string, entry fs.DirEntry, err error) error {
		if full == cwd {
			return err
		}
		if ctx.Err() != nil || len(list.files) >= sessionFilesMaxEntries {
			list.truncated = true
			return filepath.SkipAll
		}
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if entry.IsDir() && (strings.HasPrefix(name, ".") || name == "node_modules") {
			return filepath.SkipDir
		}
		rel, relErr := filepath.Rel(cwd, full)
		if relErr != nil {
			return nil
		}
		list.files = append(list.files, sessionFile{Path: filepath.ToSlash(rel), Dir: entry.IsDir()})
		return nil
	})
	return list
}

type scoredFile struct {
	file  sessionFile
	score int
}

func matchSessionFiles(files []sessionFile, query string, limit int) []sessionFile {
	query = strings.TrimSpace(query)
	var scored []scoredFile
	if query == "" {
		scored = make([]scoredFile, 0, len(files))
		for _, file := range files {
			scored = append(scored, scoredFile{file: file, score: -strings.Count(file.Path, "/")})
		}
	} else {
		initFileScoring()
		pattern := []rune(strings.ToLower(query))
		slab := util.MakeSlab(100*1024, 2048)
		for _, file := range files {
			chars := util.ToChars([]byte(file.Path))
			result, _ := algo.FuzzyMatchV2(false, true, true, &chars, pattern, false, slab)
			if result.Start >= 0 && result.Score > 0 {
				scored = append(scored, scoredFile{file: file, score: result.Score})
			}
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if len(scored[i].file.Path) != len(scored[j].file.Path) {
			return len(scored[i].file.Path) < len(scored[j].file.Path)
		}
		return scored[i].file.Path < scored[j].file.Path
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]sessionFile, len(scored))
	for i, entry := range scored {
		out[i] = entry.file
	}
	return out
}

func (d *Daemon) sessionFilesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	query := r.URL.Query()
	session, err := d.Registry.Session(query.Get("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "No such session.")
		return
	}
	limit := sessionFilesDefaultLimit
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeAPIError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive number.")
			return
		}
		limit = min(parsed, sessionFilesMaxLimit)
	}
	cwd := session.Cwd
	if cwd == "" {
		writeAPIError(w, http.StatusNotFound, "no_cwd", "This session has no working directory to list.")
		return
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		writeAPIError(w, http.StatusNotFound, "no_cwd", "This session's working directory is not on this computer.")
		return
	}
	list := d.sessionFiles.get(r.Context(), cwd, time.Now())
	writeDashboardJSON(w, http.StatusOK, map[string]any{"files": matchSessionFiles(list.files, query.Get("q"), limit), "truncated": list.truncated})
}
