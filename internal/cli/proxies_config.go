package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/surface"
)

func splitKeyCommand(args []string) ([]string, []string, error) {
	if !hasFlag(args, "--key-command") {
		return args, nil, nil
	}
	end := slices.Index(args, "--")
	if end < 0 || end == len(args)-1 {
		return nil, nil, errors.New("--key-command takes the command after --, for example --key-command -- setec get proxy-key")
	}
	return args[:end], args[end+1:], nil
}

func keyRefFromFlags(args, command []string) (*proxyacct.KeyRef, error) {
	ref := &proxyacct.KeyRef{Env: flagVal(args, "--key-env")}
	if len(command) > 0 {
		path, err := exec.LookPath(command[0])
		if err != nil {
			return nil, fmt.Errorf("the key command %s: %w", command[0], err)
		}
		if path, err = filepath.Abs(path); err != nil {
			return nil, err
		}
		ref.Command = append([]string{path}, command[1:]...)
	}
	if path := flagVal(args, "--key-file"); path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		ref.File = absolute
	}
	service, account := flagVal(args, "--keychain-service"), flagVal(args, "--keychain-account")
	if service != "" || account != "" {
		ref.Keychain = &proxyacct.Keychain{Service: service, Account: account}
	}
	count := 0
	for _, set := range []bool{ref.File != "", ref.Keychain != nil, ref.Env != "", len(ref.Command) > 0} {
		if set {
			count++
		}
	}
	switch count {
	case 0:
		return nil, nil
	case 1:
		return ref, nil
	}
	return nil, fmt.Errorf("name one key reference: --key-file, --key-env, --keychain-service with --keychain-account, or --key-command -- <argv...>")
}

func readProxyConfigs(home string) (map[string]json.RawMessage, []proxyacct.Config, error) {
	config, err := surface.ReadAgenthailConfig(home)
	if err != nil {
		return nil, nil, err
	}
	proxies, err := surface.ConfiguredProxies(home)
	return config, proxies, err
}

func writeProxyConfigs(home string, config map[string]json.RawMessage, proxies []proxyacct.Config) error {
	if len(proxies) == 0 {
		delete(config, "proxies")
	} else {
		raw, err := json.Marshal(proxies)
		if err != nil {
			return err
		}
		if _, err := proxyacct.ParseConfigs(raw); err != nil {
			return err
		}
		config["proxies"] = raw
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	path := surface.AgenthailConfigPath(home)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
