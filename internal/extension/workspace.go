// Package extension reads the extensions the person keeps under
// ~/.agenthail/extensions and the capabilities they approved for each.
package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
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
	Mounts       []Mount  `json:"mounts,omitempty"`
	App          *App     `json:"app,omitempty"`
}

// App asks the apps to offer the page or mount its own sidebar entry or tab; the person pins it or not.
type App struct {
	Label string `json:"label,omitempty"`
	Icon  string `json:"icon,omitempty"`
	Order int    `json:"order,omitempty"`
}

const maxAppLabel = 40

var iconPattern = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+)*$`)

type Mount struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Upstream string `json:"upstream"`
	Entry    string `json:"entry,omitempty"`
	// Signin is a script under assets/ that Agenthail adds to the app's pages
	// so the app sends the key placeholder, which Agenthail swaps for the key.
	Signin string `json:"signin,omitempty"`
	App    *App   `json:"app,omitempty"`
}

const maxMounts = 16

type Extension struct {
	ID       string
	Dir      string
	Manifest Manifest
	HasPage  bool
	Err      error
}

// The seeded recipes folder sits beside the extensions, so its name is taken.
func ValidID(id string) bool { return idPattern.MatchString(id) && id != recipesDir }

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
	if err := manifest.App.normalize(); err != nil {
		return Manifest{}, fmt.Errorf("%s: app: %w", ManifestFile, err)
	}
	slices.Sort(manifest.Capabilities)
	manifest.Capabilities = slices.Compact(manifest.Capabilities)
	if len(manifest.Mounts) > maxMounts {
		return Manifest{}, fmt.Errorf("%s declares more than %d mounts", ManifestFile, maxMounts)
	}
	seen := map[string]bool{}
	for i := range manifest.Mounts {
		mount := &manifest.Mounts[i]
		if err := mount.normalize(); err != nil {
			return Manifest{}, fmt.Errorf("%s: mount %q: %w", ManifestFile, mount.Name, err)
		}
		if seen[mount.Name] {
			return Manifest{}, fmt.Errorf("%s declares mount %q twice", ManifestFile, mount.Name)
		}
		seen[mount.Name] = true
	}
	return manifest, nil
}

func (m *Mount) normalize() error {
	if !idPattern.MatchString(m.Name) {
		return errors.New("a name is a lowercase letter followed by up to 39 lowercase letters, digits or dashes")
	}
	m.Label = strings.TrimSpace(m.Label)
	if m.Label == "" {
		return errors.New("needs a label")
	}
	upstream, err := url.Parse(m.Upstream)
	if err != nil || (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Host == "" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" || (upstream.Path != "" && upstream.Path != "/") {
		return errors.New("upstream must be an http or https origin, such as http://127.0.0.1:8317, with no path")
	}
	m.Upstream = upstream.Scheme + "://" + upstream.Host
	if m.Entry == "" {
		m.Entry = "/"
	}
	if !strings.HasPrefix(m.Entry, "/") || strings.HasPrefix(m.Entry, "//") {
		return errors.New("entry must be a path on the app, starting with /")
	}
	if m.Signin != "" && !strings.HasPrefix(path.Clean("/"+m.Signin), "/"+AssetsDir+"/") {
		return errors.New("signin must be a file under assets/")
	}
	if err := m.App.normalize(); err != nil {
		return fmt.Errorf("app: %w", err)
	}
	return nil
}

func (a *App) normalize() error {
	if a == nil {
		return nil
	}
	a.Label = strings.TrimSpace(a.Label)
	if len([]rune(a.Label)) > maxAppLabel {
		return fmt.Errorf("label is longer than %d characters", maxAppLabel)
	}
	a.Icon = strings.TrimSpace(a.Icon)
	if a.Icon != "" && (len(a.Icon) > 64 || !iconPattern.MatchString(a.Icon)) {
		return errors.New("icon is an SF Symbol name, such as chart.bar.xaxis")
	}
	if a.Order < -1000 || a.Order > 1000 {
		return errors.New("order is a whole number from -1000 to 1000")
	}
	return nil
}

func (e Extension) Asset(sub string) (string, bool) {
	if path.Clean("/"+sub) == "/" {
		return "", false
	}
	return e.PagePath(sub)
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
