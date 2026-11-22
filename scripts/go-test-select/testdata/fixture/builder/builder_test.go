package builder

import (
	"os/exec"
	"testing"
)

func TestBuild(t *testing.T) {
	if out, err := exec.Command("go", "build", "-o", t.TempDir(), "../app").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}
