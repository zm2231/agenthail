package main

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	os.Exit(testkit.RunInOwnHome(m.Run))
}

func TestItListsTheTestsGoTestRunsWithoutRunningThePackage(t *testing.T) {
	got, err := packageTests("./testdata/fixture")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ExampleSuite", "FuzzParse", "TestCounted", "TestDotImported", "TestFromOutside", "Test_underscore"}
	if !slices.Equal(got, want) {
		t.Fatalf("tests = %v, want %v", got, want)
	}
	raced, err := packageTests("./testdata/fixture", "-race")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(raced, "TestOnlyUnderRace") {
		t.Fatalf("with -race, tests = %v, want TestOnlyUnderRace among them", raced)
	}
}

func TestEveryTestRunsInExactlyOneShard(t *testing.T) {
	names := []string{"TestA", "TestB", "TestC", "TestD", "TestE", "TestF", "TestG", "TestH"}
	seen := map[string]int{}
	for shard := range 3 {
		for _, name := range shardOf(names, shard, 3) {
			seen[name]++
		}
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Errorf("%s ran in %d shards", name, seen[name])
		}
	}
	selected, _, err := shard([]string{"0", "1", "./testdata/fixture"})
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(pattern(selected))
	if !re.MatchString("TestCounted") || re.MatchString("TestCountedTwice") {
		t.Fatalf("pattern %q does not match whole names", pattern(selected))
	}
}
