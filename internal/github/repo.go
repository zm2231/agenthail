package github

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Repo struct {
	Host  string
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Host + "/" + r.Owner + "/" + r.Name }

type Ref struct {
	Repo
	Number int
}

func (r Ref) URL() string { return fmt.Sprintf("https://%s/pull/%d", r.Repo, r.Number) }

var segment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func newRepo(host, owner, name string) (Repo, bool) {
	host = strings.ToLower(host)
	name = strings.TrimSuffix(name, ".git")
	if host == "" || !segment.MatchString(owner) || !segment.MatchString(name) || name == "." || name == ".." {
		return Repo{}, false
	}
	return Repo{Host: host, Owner: owner, Name: name}, true
}

func ParseURL(raw string) (Ref, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.User != nil {
		return Ref{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return Ref{}, false
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 {
		return Ref{}, false
	}
	repo, ok := newRepo(u.Hostname(), parts[0], parts[1])
	return Ref{Repo: repo, Number: number}, ok
}

func ParseRepo(raw string) (Repo, bool) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Repo{}, false
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 2 {
			return Repo{}, false
		}
		return newRepo(u.Hostname(), parts[0], parts[1])
	}
	if at := strings.Index(raw, "@"); at >= 0 {
		host, path, ok := strings.Cut(raw[at+1:], ":")
		parts := strings.Split(path, "/")
		if !ok || len(parts) != 2 {
			return Repo{}, false
		}
		return newRepo(host, parts[0], parts[1])
	}
	parts := strings.Split(raw, "/")
	switch len(parts) {
	case 2:
		return newRepo("github.com", parts[0], parts[1])
	case 3:
		return newRepo(parts[0], parts[1], parts[2])
	}
	return Repo{}, false
}
