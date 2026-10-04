package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

type catalogHostProject struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	CommonDir   string `json:"commonDir,omitempty"`
	Path        string `json:"path,omitempty"`
}

type catalogCheckout struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Branch       string `json:"branch,omitempty"`
	DetachedHead string `json:"detachedHead,omitempty"`
	IsMain       bool   `json:"isMain"`
	Dirty        bool   `json:"dirty"`
}

type catalogIdentity struct {
	HostProject       catalogHostProject `json:"hostProject"`
	Checkout          catalogCheckout    `json:"checkout"`
	UnavailableReason string             `json:"unavailableReason,omitempty"`
}

func catalogIdentityForSession(ctx context.Context, session surface.Session) catalogIdentity {
	path, err := workspace.NormalizeCWD(session.Cwd)
	if err != nil || path == "" {
		return catalogIdentity{UnavailableReason: "workspace path is unavailable"}
	}
	identity := catalogIdentity{HostProject: catalogHostProject{ID: catalogPathID("project", path), DisplayName: filepath.Base(path), Path: path}, Checkout: catalogCheckout{ID: catalogPathID("checkout", path), Path: path}}
	root, err := catalogGit(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		if isCatalogNonGit(err) {
			return identity
		}
		identity.UnavailableReason = "repository identity is unavailable"
		return identity
	}
	root, err = canonicalCatalogPath(root)
	if err != nil {
		identity.UnavailableReason = "repository root is unavailable"
		return identity
	}
	identity.Checkout.Path = root
	identity.Checkout.ID = catalogPathID("checkout", root)
	identity.HostProject.DisplayName = filepath.Base(root)
	commonDir, err := catalogGit(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		identity.UnavailableReason = "repository common directory is unavailable"
		return identity
	}
	commonDir, err = canonicalCatalogPath(commonDir)
	if err != nil {
		identity.UnavailableReason = "repository common directory is unavailable"
		return identity
	}
	identity.HostProject.ID = catalogPathID("project", commonDir)
	identity.HostProject.CommonDir = commonDir
	identity.HostProject.Path = ""
	branch, err := catalogGit(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		identity.Checkout.Branch = branch
		if upstream, upstreamErr := catalogGit(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); upstreamErr == nil {
			identity.Checkout.IsMain = strings.TrimPrefix(upstream, "origin/") == branch
		} else {
			identity.Checkout.IsMain = branch == "main" || branch == "master"
		}
	} else if head, headErr := catalogGit(ctx, root, "rev-parse", "HEAD"); headErr == nil {
		identity.Checkout.DetachedHead = head
	} else {
		identity.UnavailableReason = "repository head is unavailable"
		return identity
	}
	if porcelain, statusErr := catalogGit(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all"); statusErr == nil {
		identity.Checkout.Dirty = porcelain != ""
	} else {
		identity.UnavailableReason = "repository status is unavailable"
	}
	return identity
}

func catalogGit(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func canonicalCatalogPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved), nil
	}
	if _, err := os.Stat(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func catalogPathID(prefix, path string) string {
	digest := sha256.Sum256([]byte(path))
	return prefix + "-" + hex.EncodeToString(digest[:12])
}

func isCatalogNonGit(err error) bool {
	return strings.Contains(err.Error(), "exit status 128")
}
