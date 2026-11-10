package main

import (
	"os"
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	os.Exit(testkit.RunInOwnHome(m.Run))
}

func TestItListsEveryPackageWithAMacOSFileOrARuntimeGOOSRead(t *testing.T) {
	fixtures := []string{"plain", "suffix", "tagged", "testonly", "goos", "aliased", "shadowed", "darwinonly"}
	var patterns []string
	for _, name := range fixtures {
		patterns = append(patterns, "./testdata/"+name)
	}
	found, err := darwinPackages(patterns)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, pkg := range found {
		got = append(got, pkg.ImportPath)
	}
	const prefix = "github.com/zm2231/agenthail/scripts/go-darwin-packages/testdata/"
	want := []string{prefix + "aliased", prefix + "darwinonly", prefix + "goos", prefix + "suffix", prefix + "tagged", prefix + "testonly"}
	if !slices.Equal(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
}
