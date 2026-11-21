package surfacetest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func ShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }

func PyQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func ReadLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
