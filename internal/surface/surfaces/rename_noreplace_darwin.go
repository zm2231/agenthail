package surfaces

import "golang.org/x/sys/unix"

func renameNoReplace(from, to string) error {
	return renameError(from, to, unix.RenamexNp(from, to, unix.RENAME_EXCL))
}
