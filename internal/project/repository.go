package project

import (
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

type Repository struct {
	CanonicalKey string `json:"canonicalKey"`
	RemoteName   string `json:"remoteName"`
	RemoteURL    string `json:"remoteUrl"`
	Name         string `json:"name"`
}

// Key is a project's identity across hosts: the repository, plus the
// project's folder inside it when that is not the repository root.
func Key(canonicalKey, relativePath string) string {
	relativePath = strings.Trim(path.Clean("/"+strings.ReplaceAll(relativePath, `\`, "/")), "/")
	if relativePath == "" {
		return "repo:" + canonicalKey
	}
	return "repo:" + canonicalKey + "::" + relativePath
}

// PickRemote takes origin, else the first remote by name. A local remote
// names a place on one machine, so it is skipped.
func PickRemote(urls map[string]string) (Repository, bool) {
	names := make([]string, 0, len(urls))
	for name, raw := range urls {
		if !isLocalRemote(raw) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return Repository{}, false
	}
	sort.Strings(names)
	name := names[0]
	if _, found := urls["origin"]; found && !isLocalRemote(urls["origin"]) {
		name = "origin"
	}
	key := CanonicalKey(urls[name])
	return Repository{CanonicalKey: key, RemoteName: name, RemoteURL: urls[name], Name: path.Base(key)}, true
}

var (
	urlScheme  = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://`)
	drive      = regexp.MustCompile(`^[a-z]:[\\/]`)
	webScheme  = regexp.MustCompile(`^(?:ssh|https?|git)://`)
	driveColon = regexp.MustCompile(`^[a-z]:`)
	scpForm    = regexp.MustCompile(`^(?:[a-z0-9._-]+@)?([^@:/\\\s]+):/?([^/\s]+(?:/[^/\s]+)+)$`)
)

func isLocalRemote(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case lower == "" || strings.HasPrefix(lower, "file:"):
		return true
	case urlScheme.MatchString(lower):
		return false
	case drive.MatchString(lower):
		return true
	}
	colon := strings.Index(lower, ":")
	slash := strings.IndexAny(lower, `/\`)
	return colon == -1 || (slash != -1 && slash < colon)
}

func CanonicalKey(raw string) string {
	normalized := strings.TrimSpace(raw)
	normalized = strings.TrimRight(normalized, "/")
	if len(normalized) >= 4 && strings.EqualFold(normalized[len(normalized)-4:], ".git") {
		normalized = normalized[:len(normalized)-4]
	}
	normalized = strings.ToLower(normalized)
	if webScheme.MatchString(normalized) {
		parsed, err := url.Parse(normalized)
		if err != nil {
			return normalized
		}
		segments := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
		if parsed.Hostname() != "" && len(segments) > 1 {
			if key, ok := azureKey(parsed.Hostname(), segments); ok {
				return key
			}
			return parsed.Hostname() + "/" + strings.Join(segments, "/")
		}
	}
	if !driveColon.MatchString(normalized) {
		if match := scpForm.FindStringSubmatch(normalized); match != nil {
			if key, ok := azureKey(match[1], strings.Split(match[2], "/")); ok {
				return key
			}
			return match[1] + "/" + match[2]
		}
	}
	return normalized
}

// azureKey maps Azure DevOps SSH remotes to the web spelling of the same
// repository, which shares no part with it.
func azureKey(host string, segments []string) (string, bool) {
	if host != "ssh.dev.azure.com" && host != "vs-ssh.visualstudio.com" {
		return "", false
	}
	if len(segments) != 4 || segments[0] != "v3" || segments[1] == "" || segments[2] == "" || segments[3] == "" {
		return "", false
	}
	if host == "ssh.dev.azure.com" {
		return "dev.azure.com/" + segments[1] + "/" + segments[2] + "/_git/" + segments[3], true
	}
	return segments[1] + ".visualstudio.com/" + segments[2] + "/_git/" + segments[3], true
}
