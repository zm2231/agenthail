// Package signin names the browser profile Agenthail reads sign-in cookies
// from for each consumer that needs them. Only selectors are stored; cookies
// are read from the browser at request time and never persisted.
package signin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/zm2231/agenthail/internal/installdir"
)

type Browser string

const (
	Chrome   Browser = "chrome"
	Brave    Browser = "brave"
	Arc      Browser = "arc"
	Chromium Browser = "chromium"
	Edge     Browser = "edge"
	Firefox  Browser = "firefox"
	Safari   Browser = "safari"
)

var Browsers = []Browser{Chrome, Brave, Arc, Chromium, Edge, Firefox, Safari}

const SafariProfile = "Default"

func (b Browser) Valid() bool {
	for _, known := range Browsers {
		if b == known {
			return true
		}
	}
	return false
}

func (b Browser) Label() string {
	switch b {
	case Chrome:
		return "Google Chrome"
	case Brave:
		return "Brave"
	case Arc:
		return "Arc"
	case Chromium:
		return "Chromium"
	case Edge:
		return "Microsoft Edge"
	case Firefox:
		return "Firefox"
	case Safari:
		return "Safari"
	}
	return string(b)
}

func (b Browser) chromiumFamily() bool {
	return b == Chrome || b == Brave || b == Arc || b == Chromium || b == Edge
}

type Consumer string

const (
	Notion Consumer = "notion"
	Claude Consumer = "claude"
)

type ConsumerInfo struct {
	ID      Consumer `json:"id"`
	Label   string   `json:"label"`
	Purpose string   `json:"purpose"`
}

// Consumers are the only paths that read browser cookies.
var Consumers = []ConsumerInfo{
	{ID: Notion, Label: "Notion", Purpose: "Reads and sends Notion AI threads through app.notion.com."},
	{ID: Claude, Label: "Claude Remote Control", Purpose: "Reaches Claude Code sessions attached to Remote Control through claude.ai."},
}

func LookupConsumer(id string) (ConsumerInfo, bool) {
	for _, consumer := range Consumers {
		if string(consumer.ID) == id {
			return consumer, true
		}
	}
	return ConsumerInfo{}, false
}

func (c Consumer) Label() string {
	if info, ok := LookupConsumer(string(c)); ok {
		return info.Label
	}
	return string(c)
}

// Profile is a named browser profile a consumer can be bound to. Its browser
// and profile selector never change; a different browser profile is a new
// Profile with a new ID.
type Profile struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Browser   Browser `json:"browser"`
	Profile   string  `json:"profile"`
	CreatedAt string  `json:"createdAt"`
}

type Source struct {
	ProfileID string
	Label     string
	Browser   Browser
	Dir       string
}

type Target struct {
	Consumer Consumer
}

// Resolver resolves a consumer's choice. SigninCandidates lists every
// readable profile the consumer may read, its choice first, for a consumer
// that must find the profile signed into a particular account.
type Resolver interface {
	ResolveSignin(Target) (Source, error)
	SigninCandidates(Consumer) ([]Source, error)
}

// Mode is how a consumer picks its browser profile: the system default
// browser, a profile the user chose, or no browser at all.
type Mode string

const (
	Automatic Mode = "automatic"
	Chosen    Mode = "profile"
	Off       Mode = "off"
)

// RefAutomatic and RefOff are the profile references that choose the system
// default browser and no browser.
const (
	RefAutomatic = "auto"
	RefOff       = "none"
)

// Choice is what the user decided for one consumer. Profile is set only when
// Mode is Chosen.
type Choice struct {
	Mode    Mode
	Profile *Profile
}

// Selection is the browser profile a choice resolves to.
type Selection struct {
	Mode         Mode    `json:"mode"`
	ProfileID    string  `json:"profileId,omitempty"`
	Browser      Browser `json:"browser,omitempty"`
	BrowserLabel string  `json:"browserLabel,omitempty"`
	Profile      string  `json:"profile,omitempty"`
	Name         string  `json:"name,omitempty"`
}

// Status is a consumer's choice, the browser profile it resolves to on this
// computer, and why that profile cannot be read.
type Status struct {
	ConsumerInfo
	ProfileID    *string `json:"profileId"`
	Mode         Mode    `json:"mode"`
	Browser      Browser `json:"browser,omitempty"`
	BrowserLabel string  `json:"browserLabel,omitempty"`
	Profile      string  `json:"profile,omitempty"`
	ProfileName  string  `json:"profileName,omitempty"`
	Problem      string  `json:"problem,omitempty"`
	// Unavailable says why browser sign-in cannot work on this computer at
	// all, as a limit of the computer rather than a problem to fix.
	Unavailable string `json:"unavailable,omitempty"`
}

func NewStatus(consumer ConsumerInfo, selection Selection, err error) Status {
	status := Status{ConsumerInfo: consumer, Mode: selection.Mode, Browser: selection.Browser, BrowserLabel: selection.BrowserLabel, Profile: selection.Profile, ProfileName: selection.Name}
	if selection.ProfileID != "" {
		id := selection.ProfileID
		status.ProfileID = &id
	}
	var problem ProblemError
	var unavailable UnavailableError
	switch {
	case err == nil || IsOff(err):
	case errors.As(err, &unavailable):
		status.Unavailable = unavailable.Reason()
	case errors.As(err, &problem):
		status.Problem = problem.Err.Error()
	default:
		status.Problem = err.Error()
	}
	return status
}

// ChangeHint says how to point a consumer at another profile, or back at the
// system default browser.
func ChangeHint(consumer Consumer) string {
	return fmt.Sprintf("to change it, run `agenthail signin use %s <profile|auto|none>` (see `agenthail signin list`) or open Settings > Browser Sign-in", consumer)
}

type OffError struct {
	Consumer Consumer
}

func (e OffError) Error() string {
	return fmt.Sprintf("%s: browser sign-in is turned off; to turn it back on, run `agenthail signin use %s auto` or open Settings > Browser Sign-in", e.Consumer.Label(), e.Consumer)
}

func IsOff(err error) bool {
	var target OffError
	return errors.As(err, &target)
}

// UnavailableError is a consumer left on the system default browser on a
// computer that has none, so it reads no cookies here until a profile is
// pinned.
type UnavailableError struct {
	Consumer Consumer
}

func (e UnavailableError) Reason() string {
	return fmt.Sprintf("No browser on this computer, so %s sign-in is unavailable here. If a browser is installed, pin one of its profiles with `agenthail signin use %s <profile>` (see `agenthail signin list`).", e.Consumer.Label(), e.Consumer)
}

func (e UnavailableError) Error() string {
	return fmt.Sprintf("%s: %s", e.Consumer.Label(), e.Reason())
}

func IsUnavailable(err error) bool {
	var target UnavailableError
	return errors.As(err, &target)
}

// ProblemError is why a consumer cannot read the profile it resolves to.
type ProblemError struct {
	Consumer Consumer
	Err      error
}

func (e ProblemError) Error() string {
	return fmt.Sprintf("%s: %v; %s", e.Consumer.Label(), e.Err, ChangeHint(e.Consumer))
}

func (e ProblemError) Unwrap() error { return e.Err }

type Fixed Source

func (f Fixed) ResolveSignin(Target) (Source, error) { return Source(f), nil }

func (f Fixed) SigninCandidates(Consumer) ([]Source, error) { return []Source{Source(f)}, nil }

// A nil resolver reads no browser.
func Resolve(resolver Resolver, target Target) (Source, error) {
	if resolver == nil {
		return Source{}, OffError{Consumer: target.Consumer}
	}
	return resolver.ResolveSignin(target)
}

func Candidates(resolver Resolver, consumer Consumer) ([]Source, error) {
	if resolver == nil {
		return nil, OffError{Consumer: consumer}
	}
	return resolver.SigninCandidates(consumer)
}

// CandidatesFor is the consumer's chosen source, when it resolves, followed
// by every other profile whose cookie store opens. A consumer turned off has
// none. When nothing opens, the choice's problem is returned.
func CandidatesFor(home string, consumer Consumer, choice Choice, profiles []Profile) ([]Source, error) {
	_, chosen, err := Select(home, consumer, choice)
	if IsOff(err) {
		return nil, err
	}
	var sources []Source
	seen := map[string]bool{}
	add := func(source Source) {
		key := string(source.Browser) + "\x00" + source.Dir
		if !seen[key] {
			seen[key] = true
			sources = append(sources, source)
		}
	}
	if err == nil {
		add(chosen)
	}
	for _, profile := range profiles {
		if source, sourceErr := SourceFor(home, profile); sourceErr == nil {
			add(source)
		}
	}
	if len(sources) == 0 && err != nil {
		return nil, err
	}
	return sources, nil
}

// Select resolves a consumer's choice to a browser profile and checks that
// its cookie store can be opened. The selection is filled in as far as it
// resolved, so a problem still names the browser and profile it is about.
func Select(home string, consumer Consumer, choice Choice) (Selection, Source, error) {
	selection := Selection{Mode: choice.Mode}
	var profile Profile
	switch choice.Mode {
	case Off:
		return selection, Source{}, OffError{Consumer: consumer}
	case Chosen:
		if choice.Profile == nil {
			return selection, Source{}, fmt.Errorf("%s: no sign-in profile given", consumer.Label())
		}
		profile = *choice.Profile
		selection.ProfileID = profile.ID
	case Automatic:
		var err error
		if profile, err = DefaultProfile(home); errors.Is(err, ErrNoDefaultBrowser) {
			return selection, Source{}, UnavailableError{Consumer: consumer}
		} else if err != nil {
			selection.Browser, selection.BrowserLabel = profile.Browser, profile.Browser.Label()
			return selection, Source{}, ProblemError{Consumer: consumer, Err: err}
		}
	default:
		return selection, Source{}, fmt.Errorf("%s: unknown sign-in mode %q", consumer.Label(), choice.Mode)
	}
	selection.Browser, selection.BrowserLabel, selection.Profile, selection.Name = profile.Browser, profile.Browser.Label(), profile.Profile, profile.Label
	source, err := SourceFor(home, profile)
	if err != nil {
		return selection, Source{}, ProblemError{Consumer: consumer, Err: err}
	}
	return selection, source, nil
}

// SourceFor locates a profile's directory and opens its cookie store to prove
// it is readable. The store is closed unread.
func SourceFor(home string, profile Profile) (Source, error) {
	source := Source{ProfileID: profile.ID, Label: profile.Label, Browser: profile.Browser}
	switch {
	case profile.Browser == Safari:
		if runtime.GOOS != "darwin" {
			return Source{}, fmt.Errorf("Safari is not supported on %s", runtime.GOOS)
		}
		return source, openCookieStore(profile, safariCookieStores(home))
	case profile.Browser == Firefox:
		root := firefoxRoot(home)
		dir := profile.Profile
		if !filepath.IsAbs(dir) {
			if root == "" {
				return Source{}, fmt.Errorf("Firefox is not supported on %s", runtime.GOOS)
			}
			dir = filepath.Join(root, filepath.FromSlash(dir))
		}
		source.Dir = dir
	case profile.Browser.chromiumFamily():
		root := chromiumRoot(home, profile.Browser)
		if root == "" {
			return Source{}, fmt.Errorf("%s is not supported on %s", profile.Browser.Label(), runtime.GOOS)
		}
		if !validProfileDir(profile.Profile) {
			return Source{}, fmt.Errorf("invalid %s profile %q", profile.Browser.Label(), profile.Profile)
		}
		source.Dir = filepath.Join(root, profile.Profile)
	default:
		return Source{}, fmt.Errorf("unsupported browser %q", profile.Browser)
	}
	info, err := os.Stat(source.Dir)
	if errors.Is(err, fs.ErrPermission) {
		return Source{}, permissionError(profile, source.Dir, err)
	}
	if err != nil || !info.IsDir() {
		return Source{}, fmt.Errorf("%s profile %q no longer exists at %s", profile.Browser.Label(), profile.Profile, source.Dir)
	}
	return source, openCookieStore(profile, cookieStores(profile.Browser, source.Dir))
}

func cookieStores(browser Browser, dir string) []string {
	if browser == Firefox {
		return []string{filepath.Join(dir, "cookies.sqlite")}
	}
	return []string{filepath.Join(dir, "Network", "Cookies"), filepath.Join(dir, "Cookies")}
}

// safariCookieStores lists the places Safari keeps cookies, in the order the
// cookie bridge reads them.
func safariCookieStores(home string) []string {
	return []string{
		filepath.Join(home, "Library", "Cookies", "Cookies.binarycookies"),
		filepath.Join(home, "Library", "Containers", "com.apple.Safari", "Data", "Library", "Cookies", "Cookies.binarycookies"),
	}
}

// openCookieStore succeeds when the first cookie store that exists opens. A
// store that exists but cannot be opened is a permission problem, never an
// empty browser.
func openCookieStore(profile Profile, paths []string) error {
	for _, path := range paths {
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return file.Close()
		}
		if errors.Is(err, fs.ErrPermission) {
			return permissionError(profile, path, err)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cannot open %s cookies at %s: %w", profile.Browser.Label(), path, err)
		}
	}
	if profile.Browser == Safari {
		return fmt.Errorf("Safari has no cookie store at %s; open Safari and sign in once", paths[len(paths)-1])
	}
	return fmt.Errorf("%s profile %q has no cookie store in %s; open that profile and sign in once", profile.Browser.Label(), profile.Profile, filepath.Dir(paths[len(paths)-1]))
}

// macOS privacy protection refuses with EPERM, which Full Disk Access lifts;
// file modes refuse with EACCES.
func permissionError(profile Profile, path string, err error) error {
	if runtime.GOOS != "darwin" || !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("permission denied reading %s data at %s; make it readable by the user that runs Agenthail", profile.Browser.Label(), path)
	}
	exe, exeErr := os.Executable()
	if exeErr != nil {
		exe = "the agenthail daemon"
	} else if bundle := installdir.Bundle(exe); bundle != "" {
		exe = bundle
	}
	return fmt.Errorf("permission denied reading %s data at %s (macOS needs Full Disk Access for this); add %s in System Settings > Privacy & Security > Full Disk Access, then restart Agenthail", profile.Browser.Label(), path, exe)
}

func validProfileDir(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

func chromiumRoot(home string, browser Browser) string {
	if runtime.GOOS == "darwin" {
		base := filepath.Join(home, "Library", "Application Support")
		switch browser {
		case Chrome:
			return filepath.Join(base, "Google", "Chrome")
		case Brave:
			return filepath.Join(base, "BraveSoftware", "Brave-Browser")
		case Arc:
			return filepath.Join(base, "Arc", "User Data")
		case Chromium:
			return filepath.Join(base, "Chromium")
		case Edge:
			return filepath.Join(base, "Microsoft Edge")
		}
		return ""
	}
	if runtime.GOOS == "linux" {
		base := filepath.Join(home, ".config")
		switch browser {
		case Chrome:
			return filepath.Join(base, "google-chrome")
		case Brave:
			return filepath.Join(base, "BraveSoftware", "Brave-Browser")
		case Chromium:
			return filepath.Join(base, "chromium")
		case Edge:
			return filepath.Join(base, "microsoft-edge")
		}
	}
	return ""
}

func firefoxRoot(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Firefox")
	case "linux":
		return filepath.Join(home, ".mozilla", "firefox")
	}
	return ""
}
