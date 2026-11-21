package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
)

// turnScanLimit bounds how far back a reader looks for the record that
// started the turn in progress at an offset.
var turnScanLimit int64 = 64 << 20

// turnReplayStart returns where to replay from to stand at offset with what
// a reader following the transcript from its start would hold: the start of
// the turn before the one in progress, which carries the compactions and the
// context size that preceded the current turn. Without an earlier turn it is
// the transcript's start when that is in reach, else the current turn's.
func turnReplayStart(ctx context.Context, file *os.File, offset int64, starts *turnStartSearch) (int64, bool, error) {
	current, found, err := starts.before(ctx, file, offset)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, offset <= turnScanLimit, nil
	}
	previous, found, err := starts.before(ctx, file, current)
	switch {
	case err != nil:
		return 0, false, err
	case found:
		return previous, true, nil
	case current <= turnScanLimit:
		return 0, true, nil
	}
	return current, true, nil
}

func transcriptRecordBefore(ctx context.Context, file *os.File, offset int64, candidate func([]byte) bool, match func(map[string]any) bool) (int64, bool, error) {
	return newTurnStartSearch(candidate, decodeShrunkRecord, match).before(ctx, file, offset)
}

func lineContains(needle []byte) func([]byte) bool {
	return func(line []byte) bool { return bytes.Contains(line, needle) }
}

func scanTranscriptRecords(ctx context.Context, file *os.File, from, to int64, compact bool, visit func(record map[string]any, offset int64, line []byte)) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, from, to-from), 1<<20)
	offset := from
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			decoded := line
			if compact {
				decoded = shrinkTranscriptLine(line)
			}
			var record map[string]any
			if json.Unmarshal(decoded, &record) == nil {
				visit(record, offset, line)
			}
		}
		offset += int64(len(line))
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
