package surfaces

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

type visitedLine struct {
	start int64
	line  string
}

func referenceReverseLines(data []byte, floor, offset int64) []visitedLine {
	var lines []visitedLine
	end := offset
	for end > floor {
		start := int64(bytes.LastIndexByte(data[:end-1], '\n') + 1)
		if start <= floor && start != 0 {
			break
		}
		lines = append(lines, visitedLine{start, string(data[start:end])})
		end = start
	}
	return lines
}

func TestReverseTranscriptLinesVisitEveryLineWhateverItsLength(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	sizes := []int{1, 40, reverseLineChunk - 1, reverseLineChunk, reverseLineChunk + 1, 3*reverseLineChunk + 7}
	var data []byte
	var starts []int64
	for index := 0; index < 24; index++ {
		starts = append(starts, int64(len(data)))
		data = append(append(data, bytes.Repeat([]byte{'a' + byte(index%26)}, sizes[rng.Intn(len(sizes))])...), '\n')
	}
	path := filepath.Join(t.TempDir(), "lines.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	size := int64(len(data))
	for sample := 0; sample < 200; sample++ {
		offset := size
		switch sample % 3 {
		case 1:
			offset = starts[rng.Intn(len(starts))]
		case 2:
			offset = 1 + rng.Int63n(size)
		}
		floor := int64(0)
		if sample%2 == 1 {
			floor = rng.Int63n(offset + 1)
		}
		var got []visitedLine
		if err := reverseTranscriptLines(context.Background(), file, floor, offset, func(start int64, line []byte) bool {
			got = append(got, visitedLine{start, string(line)})
			return true
		}); err != nil {
			t.Fatal(err)
		}
		want := referenceReverseLines(data, floor, offset)
		if len(got) != len(want) {
			t.Fatalf("floor=%d offset=%d: visited %d lines, want %d", floor, offset, len(got), len(want))
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("floor=%d offset=%d line %d: got start %d len %d, want start %d len %d", floor, offset, index, got[index].start, len(got[index].line), want[index].start, len(want[index].line))
			}
		}
	}
}
