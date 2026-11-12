package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
	selected, _, err := shard([]string{"0", "1", "./testdata/fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(pattern(selected))
	if !re.MatchString("TestCounted") || re.MatchString("TestCountedTwice") {
		t.Fatalf("pattern %q does not match whole names", pattern(selected))
	}
}

func TestRecordedSecondsBalanceTheShards(t *testing.T) {
	seconds := map[string]float64{"TestSlowA": 50, "TestSlowB": 49, "TestMid": 10, "TestQuick1": 1, "TestQuick2": 1, "TestQuick3": 1}
	names := []string{"TestMid", "TestNew", "TestQuick1", "TestQuick2", "TestQuick3", "TestSlowA", "TestSlowB"}
	seen := map[string]int{}
	var totals []float64
	for shard := range 3 {
		selected, total := balancedShardOf(names, seconds, shard, 3)
		for _, name := range selected {
			seen[name]++
		}
		totals = append(totals, total)
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Errorf("%s ran in %d shards", name, seen[name])
		}
	}
	if !slices.Equal(totals, []float64{50, 49, 23}) {
		t.Fatalf("shard totals = %v, want the two slow tests apart and the rest together, TestNew counted at the median of 10s", totals)
	}
}

func TestRecordWritesEachTopLevelTestsSecondsForThePackage(t *testing.T) {
	in := strings.Join([]string{
		`{"Action":"run","Package":"p","Test":"TestA"}`,
		`{"Action":"pass","Package":"p","Test":"TestA/sub","Elapsed":3}`,
		`{"Action":"pass","Package":"p","Test":"TestA","Elapsed":4.5}`,
		`{"Action":"fail","Package":"p","Test":"TestB","Elapsed":9}`,
		`{"Action":"skip","Package":"p","Test":"TestC","Elapsed":0}`,
		`{"Action":"pass","Package":"other","Test":"TestD","Elapsed":7}`,
		`{"Action":"pass","Package":"p","Elapsed":14}`,
	}, "\n")
	var out bytes.Buffer
	if err := record(strings.NewReader(in), &out, "p"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "9.00\tTestB\n4.50\tTestA\n"; got != want {
		t.Fatalf("recorded %q, want %q", got, want)
	}
	path := filepath.Join(t.TempDir(), "seconds.tsv")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := readSeconds(path)
	if err != nil {
		t.Fatal(err)
	}
	if read["TestB"] != 9 || math.Abs(read["TestA"]-4.5) > 1e-9 || len(read) != 2 {
		t.Fatalf("read back %v", read)
	}
	if err := record(strings.NewReader(`{"Action":"pass","Package":"other","Test":"TestD","Elapsed":7}`), &out, "p"); err == nil {
		t.Fatal("a recording with no test of the package succeeded")
	}
}

func TestASecondsFileWithAnUnusableLineIsRefused(t *testing.T) {
	for _, line := range []string{"NaN\tTestA", "Inf\tTestA", "-1\tTestA", "1\tTestA\textra", "1\t", "TestA", "x\tTestA"} {
		path := filepath.Join(t.TempDir(), "seconds.tsv")
		if err := os.WriteFile(path, []byte("2\tTestB\n"+line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSeconds(path); err == nil {
			t.Errorf("a seconds file with the line %q was accepted", line)
		}
	}
}
