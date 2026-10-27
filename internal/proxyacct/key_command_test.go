package proxyacct

import (
	"context"
	"strings"
	"testing"
	"time"
)

const (
	commandKey   = "command-management-key"
	printTheKey  = "printf 'command-management-%s\\n' key"
	printToError = "printf 'command-management-%s\\n' key >&2"
)

func readCommand(t *testing.T, script string) (string, error) {
	t.Helper()
	ref := &KeyRef{Command: []string{"/bin/sh", "-c", script}}
	return ref.Read(context.Background())
}

func TestAKeyCommandsTrimmedOutputIsTheKey(t *testing.T) {
	key, err := readCommand(t, "printf ' '; "+printTheKey+"; echo noise >&2")
	if err != nil || key != commandKey {
		t.Fatalf("key=%q err=%v", key, err)
	}
}

func TestAFailingKeyCommandNeverCarriesItsOutput(t *testing.T) {
	_, err := readCommand(t, printTheKey+"; "+printToError+"; exit 3")
	if ProblemOf(err) != KeyUnreadable || !strings.Contains(err.Error(), "exited with status 3") || strings.Contains(err.Error(), commandKey) {
		t.Fatalf("err=%v", err)
	}
}

func TestAKeyCommandThatHangsIsStoppedWithItsChildren(t *testing.T) {
	keyCommandTimeout = 200 * time.Millisecond
	t.Cleanup(func() { keyCommandTimeout = 10 * time.Second })
	started := time.Now()
	_, err := readCommand(t, printTheKey+"; sleep 30 & sleep 30")
	if ProblemOf(err) != KeyUnreadable || !strings.Contains(err.Error(), "did not finish within") || strings.Contains(err.Error(), commandKey) {
		t.Fatalf("err=%v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("the hung command held the read for %s", elapsed)
	}
}

func TestAKeyCommandWithNoOutputOrInputIsUnreadable(t *testing.T) {
	for _, script := range []string{"exit 0", "cat"} {
		if _, err := readCommand(t, script); ProblemOf(err) != KeyUnreadable || !strings.Contains(err.Error(), "is empty") {
			t.Fatalf("%s: err=%v", script, err)
		}
	}
}
