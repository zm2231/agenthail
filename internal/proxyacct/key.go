package proxyacct

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Keychain struct {
	Service string `json:"service"`
	Account string `json:"account"`
}

// KeyRef names where a management key is kept; the key itself never enters
// the Agenthail config.
type KeyRef struct {
	File     string    `json:"file,omitempty"`
	Keychain *Keychain `json:"keychain,omitempty"`
	Env      string    `json:"env,omitempty"`
}

func (r *KeyRef) set() int {
	if r == nil {
		return 0
	}
	count := 0
	for _, set := range []bool{r.File != "", r.Keychain != nil, r.Env != ""} {
		if set {
			count++
		}
	}
	return count
}

func (r *KeyRef) Describe() string {
	switch {
	case r == nil || r.set() == 0:
		return "no key"
	case r.File != "":
		return "the key file " + r.File
	case r.Keychain != nil:
		return fmt.Sprintf("the keychain item %s/%s", r.Keychain.Service, r.Keychain.Account)
	}
	return "the environment variable " + r.Env
}

const keychainTimeout = 5 * time.Second

func (r *KeyRef) Read(ctx context.Context) (string, error) {
	if r == nil || r.set() == 0 {
		return "", &Error{Problem: KeyMissing, Err: fmt.Errorf("no management key is configured")}
	}
	key, err := r.read(ctx)
	if err != nil {
		return "", &Error{Problem: KeyUnreadable, Err: fmt.Errorf("could not read %s: %w", r.Describe(), err)}
	}
	if key = strings.TrimSpace(key); key == "" {
		return "", &Error{Problem: KeyUnreadable, Err: fmt.Errorf("%s is empty", r.Describe())}
	}
	return key, nil
}

func (r *KeyRef) read(ctx context.Context) (string, error) {
	switch {
	case r.File != "":
		info, err := os.Stat(r.File)
		if err != nil {
			return "", err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("it must be readable only by its owner (chmod 600 %s)", r.File)
		}
		data, err := os.ReadFile(r.File)
		return string(data), err
	case r.Keychain != nil:
		ctx, cancel := context.WithTimeout(ctx, keychainTimeout)
		defer cancel()
		var stdout, stderr bytes.Buffer
		command := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", r.Keychain.Service, "-a", r.Keychain.Account, "-w")
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil {
			return "", fmt.Errorf("security find-generic-password: %w", err)
		}
		return stdout.String(), nil
	}
	return os.Getenv(r.Env), nil
}
