package registry

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const (
	compressedBackupSuffix = ".zst"
	sqliteHeaderSize       = 100
	sqliteHeaderMagic      = "SQLite format 3\x00"
	sqliteUserVersionAt    = 60
)

func compressedBackupPath(path string, target int) string {
	return migrationBackupPath(path, target) + compressedBackupSuffix
}

// existingMigrationBackup is the copy for a schema, or "" when there is none.
func existingMigrationBackup(path string, target int) string {
	for _, file := range []string{compressedBackupPath(path, target), migrationBackupPath(path, target)} {
		if _, err := os.Stat(file); err == nil {
			return file
		}
	}
	return ""
}

func compressMigrationBackups(path string) error {
	var failures []error
	for _, target := range migrationBackups(path) {
		failures = append(failures, compressBackup(migrationBackupPath(path, target)))
	}
	return errors.Join(failures...)
}

// A copy for one schema in both forms means a crash after the rename, so the compressed one is whole.
func compressBackup(plain string) error {
	compressed := plain + compressedBackupSuffix
	temporary := compressed + ".tmp"
	_ = os.Remove(temporary)
	if _, err := os.Stat(plain); os.IsNotExist(err) {
		return nil
	}
	if _, err := os.Stat(compressed); err == nil {
		return os.Remove(plain)
	}
	defer os.Remove(temporary)
	if err := writeCompressedCopy(plain, temporary); err != nil {
		return fmt.Errorf("compress %s: %w", plain, err)
	}
	if err := os.Rename(temporary, compressed); err != nil {
		return fmt.Errorf("compress %s: %w", plain, err)
	}
	if err := syncDirectory(filepath.Dir(compressed)); err != nil {
		return fmt.Errorf("compress %s: %w", plain, err)
	}
	return os.Remove(plain)
}

func writeCompressedCopy(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer destination.Close()
	encoder, err := zstd.NewWriter(destination, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return err
	}
	if _, err := io.Copy(encoder, source); err != nil {
		return errors.Join(err, encoder.Close())
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	return destination.Close()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// backupSchema is the schema of a pre-migration copy in either form, or -1 when it cannot be read.
func backupSchema(file string) int {
	if !strings.HasSuffix(file, compressedBackupSuffix) {
		return schemaOf(file)
	}
	source, err := os.Open(file)
	if err != nil {
		return -1
	}
	defer source.Close()
	decoder, err := zstd.NewReader(source, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return -1
	}
	defer decoder.Close()
	return schemaInHeader(decoder)
}

func schemaInHeader(database io.Reader) int {
	header := make([]byte, sqliteHeaderSize)
	if _, err := io.ReadFull(database, header); err != nil || string(header[:len(sqliteHeaderMagic)]) != sqliteHeaderMagic {
		return -1
	}
	return int(int32(binary.BigEndian.Uint32(header[sqliteUserVersionAt:])))
}
