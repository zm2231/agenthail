package surfaces

import (
	"fmt"
	"os"
	"reflect"

	"github.com/zm2231/agenthail/internal/surface"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// sameTranscriptState reports whether two stats of a transcript show the same
// file at the same size, modification time and change time. Every write moves
// the change time, which no one can set back as they can the modification
// time.
func sameTranscriptState(a, b os.FileInfo) bool {
	return surface.Stamp(a).Equal(surface.Stamp(b)) && transcriptFileIdentity(a) == transcriptFileIdentity(b) && transcriptChangeTime(a) == transcriptChangeTime(b)
}

// transcriptChangeTime is a stat's inode change time, or "" where the platform
// does not report one.
func transcriptChangeTime(info os.FileInfo) string {
	if info.Sys() == nil {
		return ""
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return ""
	}
	for _, name := range []string{"Ctimespec", "Ctim"} {
		if field := value.FieldByName(name); field.IsValid() && field.CanInterface() {
			return fmt.Sprintf("%v", field.Interface())
		}
	}
	return ""
}
