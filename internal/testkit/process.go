package testkit

import (
	"fmt"
	"os"
)

// WhileTestRuns is a shell loop for a fake long-running process that ends
// once this test binary is gone, so a test that is killed or times out before
// its cleanup runs leaves nothing behind.
func WhileTestRuns(interval string) string {
	return fmt.Sprintf("while kill -0 %d 2>/dev/null; do sleep %s; done\n", os.Getpid(), interval)
}
