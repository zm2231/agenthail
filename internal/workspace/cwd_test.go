package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeCWDCanonicalizesExistingSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real", "project")
	if err := os.MkdirAll(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "real"), alias); err != nil {
		t.Fatal(err)
	}
	got, err := NormalizeCWD(filepath.Join(alias, "project"))
	want, wantErr := NormalizeCWD(real)
	if err != nil || wantErr != nil || got != want {
		t.Fatalf("got=%q err=%v want=%q wantErr=%v", got, err, want, wantErr)
	}
}

func TestIsWithinUsesDirectoryBoundariesAndCanonicalPaths(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace-other"), 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		candidate string
		want      bool
	}{
		{"root", workspace, true},
		{"nested", filepath.Join(workspace, "nested"), true},
		{"canonical alias", filepath.Join(alias, "nested"), true},
		{"prefix collision", filepath.Join(root, "workspace-other"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := IsWithin(workspace, test.candidate)
			if err != nil || got != test.want {
				t.Fatalf("got=%t err=%v want=%t", got, err, test.want)
			}
		})
	}
}
