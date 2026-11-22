package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func helperFiles(t *testing.T, root string) []string {
	t.Helper()
	script, err := os.ReadFile(filepath.Join(root, "scripts", "test-native-helpers.sh"))
	if err != nil {
		t.Fatal(err)
	}
	_, list, opened := strings.Cut(string(script), "\nSOURCES=(\n")
	list, _, closed := strings.Cut(list, "\n)\n")
	if !opened || !closed {
		t.Fatal("scripts/test-native-helpers.sh has no SOURCES=( ... ) list")
	}
	files := []string{"scripts/test-native-helpers.sh"}
	for _, source := range strings.Fields(list) {
		files = append(files, "native/"+source)
	}
	tests, err := filepath.Glob(filepath.Join(root, "native", "tests", "*_test.swift"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		files = append(files, "native/tests/"+filepath.Base(test))
	}
	if len(files) < 3 {
		t.Fatalf("found only %v as native helper inputs", files)
	}
	return files
}

func TestTheNativeHelperSuiteNamesEveryFileItsTestsCompile(t *testing.T) {
	root := filepath.Join("..", "..")
	inputs := loadInputs(t, filepath.Join(root, inputsFile))
	for _, file := range helperFiles(t, root) {
		named := false
		for _, in := range inputs {
			named = named || in.scope == scopeSuite && in.target == "native-helpers" && match(in.pattern, file)
		}
		if !named {
			t.Errorf("scripts/test-native-helpers.sh compiles %s, which no native-helpers line in %s names", file, inputsFile)
		}
	}
}
