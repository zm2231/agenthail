package surfaces

import (
	"bytes"
	"fmt"
	"os"
)

func processEnv(pid int, name string) string {
	environ, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return ""
	}
	return envValue(bytes.Split(environ, []byte{0}), name)
}
