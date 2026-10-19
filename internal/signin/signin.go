// Package signin names the browser profile Agenthail reads sign-in cookies
// from for each consumer that needs them. Only selectors are stored; cookies
// are read from the browser at request time and never persisted.
package signin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	Browser   Browser
	Dir       string
}

type Target struct {
	Consumer  Consumer
	SessionID string
}

type Resolver interface {
	ResolveSignin(Target) (Source, error)
}

func SetupHint(consumer Consumer) string {
	return fmt.Sprintf("run `agenthail signin use %s <profile>` (see `agenthail signin list`) or open Settings > Browser Sign-in", consumer)
}

type NotConfiguredError struct {
	Consumer Consumer
}

func (e NotConfiguredError) Error() string {
	return fmt.Sprintf("%s: choose a browser sign-in profile; %s", e.Consumer.Label(), SetupHint(e.Consumer))
}

func IsNotConfigured(err error) bool {
	var target NotConfiguredError
	return errors.As(err, &target)
}

type Fixed Source

func (f Fixed) ResolveSignin(Target) (Source, error) { return Source(f), nil }

// A nil resolver has nothing configured.
func Resolve(resolver Resolver, target Target) (Source, error) {
	if resolver == nil {
		return Source{}, NotConfiguredError{Consumer: target.Consumer}
	}
	return resolver.ResolveSignin(target)
}

func SourceFor(home string, profile Profile) (Source, error) {
	source := Source{ProfileID: profile.ID, Browser: profile.Browser}
	switch {
	case profile.Browser == Safari:
		return source, nil
	case profile.Browser == Firefox:
		root := firefoxRoot(home)
		dir := profile.Profile
		if !filepath.IsAbs(dir) {
			if root == "" {
				return Source{}, fmt.Errorf("Firefox is not supported on %s", runtime.GOOS)
			}
			dir = filepath.Join(root, dir)
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
	if info, err := os.Stat(source.Dir); err != nil || !info.IsDir() {
		return Source{}, fmt.Errorf("%s profile %q (%s) no longer exists at %s", profile.Browser.Label(), profile.Profile, profile.Label, source.Dir)
	}
	return source, nil
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
