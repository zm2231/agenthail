package project

import (
	"net/url"
	"path"
	"strings"
)

const (
	remotePrefix = "remote:"
	commitPrefix = "commit:"
	pathPrefix   = "path:"
)

// RemoteKey reports false for a local path or file URL: it names a place on
// one machine, not the repository.
func RemoteKey(raw string) (string, bool) {
	normalized, ok := NormalizeRemote(raw)
	if !ok {
		return "", false
	}
	return remotePrefix + normalized, true
}

func CommitKey(sha string) string { return commitPrefix + strings.ToLower(sha) }

func PathKey(commonDir string) string { return pathPrefix + commonDir }

func Portable(key string) bool {
	return strings.HasPrefix(key, remotePrefix) || strings.HasPrefix(key, commitPrefix)
}

func RepoName(key string) string {
	remote, ok := strings.CutPrefix(key, remotePrefix)
	if !ok {
		return ""
	}
	return path.Base(remote)
}

// NormalizeRemote makes https://github.com/a/b.git, ssh://git@github.com:22/a/b
// and git@github.com:a/b all github.com/a/b.
func NormalizeRemote(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") {
		return "", false
	}
	var host, repoPath string
	if scheme, _, found := strings.Cut(raw, "://"); found {
		if strings.EqualFold(scheme, "file") {
			return "", false
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" {
			return "", false
		}
		host, repoPath = parsed.Hostname(), parsed.Path
	} else {
		authority, rest, found := strings.Cut(raw, ":")
		if !found || strings.Contains(authority, "/") {
			return "", false
		}
		if _, after, hasUser := strings.Cut(authority, "@"); hasUser {
			authority = after
		}
		host, repoPath = authority, rest
	}
	repoPath = strings.Trim(repoPath, "/")
	repoPath = strings.TrimSuffix(repoPath, ".git")
	repoPath = strings.TrimRight(repoPath, "/")
	if host == "" || repoPath == "" {
		return "", false
	}
	return strings.ToLower(host) + "/" + repoPath, true
}
