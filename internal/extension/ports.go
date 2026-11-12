package extension

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/atomicfile"
)

// Published records that Agenthail put the port in Tailscale Serve itself;
// only such routes are ever removed.
type MountPort struct {
	Port      int  `json:"port"`
	Published bool `json:"published,omitempty"`
}

func MountKey(id, mount string) string { return id + "/" + mount }

func mountPortsPath(home string) string { return filepath.Join(DataDir(home), "mounts.json") }

func ReadMountPorts(home string) (map[string]MountPort, error) {
	data, err := os.ReadFile(mountPortsPath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]MountPort{}, nil
	}
	if err != nil {
		return nil, err
	}
	ports := map[string]MountPort{}
	if err := json.Unmarshal(data, &ports); err != nil {
		return nil, err
	}
	return ports, nil
}

func WriteMountPorts(home string, ports map[string]MountPort) error {
	data, err := json.MarshalIndent(ports, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(DataDir(home), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(mountPortsPath(home), append(data, '\n'), 0o600)
}
