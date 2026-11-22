package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var fixture = filepath.Join("testdata", "fixture")

func fixtureGraph(t *testing.T) graph {
	t.Helper()
	g, err := loadGraph(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func parseInputs(t *testing.T, lines ...string) []input {
	t.Helper()
	inputs, err := readInputs(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}

func selected(t *testing.T, g graph, inputs []input, changed ...string) selection {
	t.Helper()
	s, err := selectTests(g, inputs, changed)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func short(s selection) []string {
	var names []string
	for _, pkg := range s.packages {
		names = append(names, strings.TrimPrefix(pkg, "example.com/fixture/"))
	}
	for _, suite := range s.suites {
		names = append(names, "suite:"+suite)
	}
	return names
}

func TestAChangedPackageSelectsItsTestsAndEveryTestThatImportsIt(t *testing.T) {
	s := selected(t, fixtureGraph(t), nil, "lib/lib.go")
	if got := short(s); s.all || !slices.Equal(got, []string{"app", "lib"}) {
		t.Fatalf("a change to lib selected %v (all=%v), want app and lib", got, s.all)
	}
}

func TestAFileOnlyOneTestReadsSelectsOnlyThatTest(t *testing.T) {
	s := selected(t, fixtureGraph(t), parseInputs(t, "data/**\ttests\treader"), "data/notes.txt")
	if got := short(s); s.all || !slices.Equal(got, []string{"reader"}) {
		t.Fatalf("a change to the notes reader reads selected %v (all=%v), want reader", got, s.all)
	}
}

func TestATestThatBuildsAPackageIsSelectedWhenAnythingThatPackageImportsChanges(t *testing.T) {
	s := selected(t, fixtureGraph(t), parseInputs(t, "app\tbuild\tbuilder"), "lib/lib.go")
	if got := short(s); !slices.Equal(got, []string{"app", "builder", "lib"}) {
		t.Fatalf("a change to lib, which app imports and builder builds, selected %v", got)
	}
}

func TestAFileNoInputNamesSelectsEverything(t *testing.T) {
	g := fixtureGraph(t)
	for name, inputs := range map[string][]input{
		"no inputs":           nil,
		"only a reading line": parseInputs(t, "**\timports\tapp", "**\tbuild\tbuilder"),
	} {
		s := selected(t, g, inputs, "lib/lib.go", "notes.md")
		if got := short(s); !s.all || !slices.Equal(got, []string{"app", "builder", "lib", "other", "reader"}) {
			t.Errorf("%s: a file outside every package selected %v (all=%v), want every package", name, got, s.all)
		}
	}
}

func TestANoneInputSelectsNothingAndAnAllInputSelectsEveryPackageAndSuite(t *testing.T) {
	g := fixtureGraph(t)
	inputs := parseInputs(t, "*.md\tnone\t-", "go.mod\tall\t-", "data/**\tsuite\tdocs")
	if s := selected(t, g, inputs, "notes.md"); s.all || len(short(s)) != 0 {
		t.Fatalf("a file no test reads selected %v (all=%v)", short(s), s.all)
	}
	if s := selected(t, g, inputs, "data/notes.txt"); s.all || !slices.Equal(short(s), []string{"suite:docs"}) {
		t.Fatalf("a suite's input selected %v (all=%v)", short(s), s.all)
	}
	s := selected(t, g, inputs, "go.mod")
	if got := short(s); !s.all || !slices.Equal(got, []string{"app", "builder", "lib", "other", "reader", "suite:docs"}) {
		t.Fatalf("an all input selected %v (all=%v)", got, s.all)
	}
}

func TestTheReachCheckNamesEveryReadOutsideAPackageThatNoInputDeclares(t *testing.T) {
	g := fixtureGraph(t)
	missing, checked, err := uncovered(fixture, g, nil)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || len(missing) != 2 || !strings.Contains(missing[0], "builder/builder_test.go:9 reads app") || !strings.Contains(missing[1], "reader/reader_test.go:10 reads data/notes.txt") {
		t.Fatalf("checked %d reads, missing %q", checked, missing)
	}
	declared := parseInputs(t, "app\tbuild\tbuilder", "data/**\ttests\treader")
	if missing, _, err := uncovered(fixture, g, declared); err != nil || len(missing) != 0 {
		t.Fatalf("with both reads declared, missing %q (err %v)", missing, err)
	}
}

func TestTheRepositorysCIInputsSelectTheTestsThatUseThem(t *testing.T) {
	root := filepath.Join("..", "..")
	g, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	inputs := loadInputs(t, filepath.Join(root, inputsFile))
	for changed, want := range map[string]string{
		"push-relay/test/relay.test.js":                     "internal/testkit",
		"internal/apitime/new_test.go":                      "internal/testkit",
		"claude-host/test/host.test.mjs":                    "internal/testkit",
		"scripts/go-test-shard/seconds/internal-daemon.tsv": "internal/daemon",
		"scripts/ci-concurrently.sh":                        "internal/daemon",
		"scripts/fetch-node.sh":                             "internal/daemon",
		"native/macOS/TranscriptFollow.swift":               "internal/uiconf",
		"skills/agenthail-operations/SKILL.md":              "internal/cli",
	} {
		s, err := selectTests(g, inputs, []string{changed})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(s.packages, g.importPath(want)) {
			t.Errorf("a change to %s selects %v, without %s", changed, short(s), want)
		}
	}
	for _, changed := range []string{"scripts/ci-go-select.sh", "go.mod", ".github/workflows/ci.yml", "scripts/go-test-select/inputs.tsv"} {
		if s, err := selectTests(g, inputs, []string{changed}); err != nil || !s.all {
			t.Errorf("a change to %s does not select everything (err %v)", changed, err)
		}
	}
}
