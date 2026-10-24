package surface

import (
	"fmt"
	"os"
	"time"
)

type FileStamp struct {
	Size     int64
	Modified time.Time
}

func Stamp(info os.FileInfo) FileStamp {
	return FileStamp{Size: info.Size(), Modified: info.ModTime()}
}

func (s FileStamp) String() string {
	return fmt.Sprintf("%d:%d", s.Size, s.Modified.UnixNano())
}

func (s FileStamp) Equal(other FileStamp) bool {
	return s.Size == other.Size && s.Modified.Equal(other.Modified)
}

func (s FileStamp) UnchangedSince(readAt time.Time, current os.FileInfo) bool {
	return s.Size == current.Size() && UnchangedSince(readAt, s.Modified, current.ModTime())
}

// MtimeGranularity bounds how coarse a file system's modification times are.
// A change in the same tick as an earlier read leaves the time unchanged.
const MtimeGranularity = 2 * time.Second

// UnchangedSince reports whether a path read at readAt, when its modification
// time was modified, is still unchanged now that its time is current. It is
// unchanged only once the read came more than MtimeGranularity after the
// change it saw; a read in the same tick as a change could have missed a
// later change in that tick.
func UnchangedSince(readAt, modified, current time.Time) bool {
	return modified.Equal(current) && readAt.Sub(modified) > MtimeGranularity
}
