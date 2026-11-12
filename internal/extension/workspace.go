// Package extension reads the extensions the person keeps under
// ~/.agenthail/extensions and the capabilities they approved for each.
package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const (
	ManifestFile = "extension.json"
	ServerFile   = "server.ts"
	PageFile     = "ui.html"
	AssetsDir    = "assets"
	maxManifest  = 64 << 10
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// read.all also subscribes the extension to every daemon event.
var Capabilities = []string{"read.all", "control.all", "requests.answer"}

func Workspace(home string) string { return filepath.Join(home, ".agenthail", "extensions") }

func DataDir(home string) string { return filepath.Join(home, ".agenthail", "extensions-data") }

func StorePath(home string) string { return filepath.Join(DataDir(home), "store.db") }

type Manifest struct {
	Label        string   `json:"label"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type Extension struct {
	ID       string
	Dir      string
	Manifest Manifest
	HasPage  bool
	Err      error
}

func ValidID(id string) bool { return idPattern.MatchString(id) }

func Discover(workspace string) ([]Extension, error) {
	entries, err := os.ReadDir(workspace)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var found []Extension
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !ValidID(entry.Name()) {
			continue
		}
		dir := filepath.Join(workspace, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, ManifestFile)); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		found = append(found, read(entry.Name(), dir))
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	return found, nil
}

func read(id, dir string) Extension {
	ext := Extension{ID: id, Dir: dir}
	manifest, err := readManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		ext.Err = err
		return ext
	}
	ext.Manifest = manifest
	if info, err := os.Stat(filepath.Join(dir, ServerFile)); err != nil || !info.Mode().IsRegular() {
		ext.Err = fmt.Errorf("%s is missing", ServerFile)
		return ext
	}
	if info, err := os.Stat(filepath.Join(dir, PageFile)); err == nil && info.Mode().IsRegular() {
		ext.HasPage = true
	}
	return ext
}

func readManifest(file string) (Manifest, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Manifest{}, err
	}
	if len(data) > maxManifest {
		return Manifest{}, fmt.Errorf("%s is larger than %d bytes", ManifestFile, maxManifest)
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	manifest.Label = strings.TrimSpace(manifest.Label)
	if manifest.Label == "" {
		return Manifest{}, fmt.Errorf("%s needs a label", ManifestFile)
	}
	for _, capability := range manifest.Capabilities {
		if !slices.Contains(Capabilities, capability) {
			return Manifest{}, fmt.Errorf("%s declares unknown capability %q; known: %s", ManifestFile, capability, strings.Join(Capabilities, ", "))
		}
	}
	slices.Sort(manifest.Capabilities)
	manifest.Capabilities = slices.Compact(manifest.Capabilities)
	return manifest, nil
}

// Only the page and files under assets/ are served; server.ts never is.
func (e Extension) PagePath(sub string) (string, bool) {
	clean := path.Clean("/" + sub)
	if clean == "/" {
		if !e.HasPage {
			return "", false
		}
		return filepath.Join(e.Dir, PageFile), true
	}
	if !strings.HasPrefix(clean, "/"+AssetsDir+"/") {
		return "", false
	}
	file := filepath.Join(e.Dir, filepath.FromSlash(clean))
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", false
	}
	root, err := filepath.EvalSymlinks(filepath.Join(e.Dir, AssetsDir))
	if err != nil || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", false
	}
	if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return resolved, true
}
