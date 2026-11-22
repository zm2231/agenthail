package dashconfig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
)

const StandardListen = "127.0.0.1:7412"

// DefaultListen moves only in tests, which must not reach a person's own Agenthail.
var DefaultListen = StandardListen

const (
	defaultCodexRecentHours = 5
	maximumCodexRecentHours = 168
)

type Config struct {
	Enabled          bool                `json:"enabled"`
	Listen           string              `json:"listen"`
	CodexRecentHours int                 `json:"codexRecentHours"`
	BusyDelivery     deliverypolicy.Mode `json:"busyDelivery"`
	RemoteAccess     RemoteAccess        `json:"remoteAccess"`
	// ResumeAfterLimit turns off resuming sessions a usage limit stopped
	// when it is false; unset leaves it on.
	ResumeAfterLimit *bool `json:"resumeAfterLimit,omitempty"`
	// MoveAfterLimit turns off moving a session a usage limit stopped to a
	// proxy root with room when it is false; unset leaves it on.
	MoveAfterLimit *bool           `json:"moveAfterLimit,omitempty"`
	Claude         *ClaudeSettings `json:"claude,omitempty"`
	// AskEscalateMinutes is how long an agent holds an ask before it passes up; unset is 10.
	AskEscalateMinutes int `json:"askEscalateMinutes,omitempty"`
	// AskPermissionEscalateMinutes is the same for a permission request, which holds a tool call; unset is 3.
	AskPermissionEscalateMinutes int `json:"askPermissionEscalateMinutes,omitempty"`
	// SimulatorHost names the paired Mac a host without simulators forwards to; unset picks the only connected one that runs them.
	SimulatorHost string `json:"simulatorHost,omitempty"`
	// NotificationPreferences holds the person's choices that differ from each preference's default.
	NotificationPreferences map[string]bool `json:"notificationPreferences,omitempty"`
	Agents                  *AgentPolicy    `json:"agents,omitempty"`
}

// ResumesAfterLimit reports whether sessions a usage limit stopped resume
// when it resets.
func (c Config) ResumesAfterLimit() bool {
	return c.ResumeAfterLimit == nil || *c.ResumeAfterLimit
}

// MovesAfterLimit reports whether a session a usage limit stopped moves to a
// proxy root with room before its own reset.
func (c Config) MovesAfterLimit() bool {
	return c.MoveAfterLimit == nil || *c.MoveAfterLimit
}

func Path() string {
	return filepath.Join(filepath.Dir(daemonctl.PidFilePath()), "dashboard.json")
}
func TokenPath() string {
	return filepath.Join(filepath.Dir(daemonctl.PidFilePath()), "dashboard.token")
}

func Load() (Config, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return Config{Listen: DefaultListen, CodexRecentHours: defaultCodexRecentHours, BusyDelivery: deliverypolicy.Queue, RemoteAccess: NormalizeRemoteAccess(RemoteAccess{})}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read dashboard config: %w", err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("parse dashboard config: %w", err)
	}
	if config.Listen == "" {
		config.Listen = DefaultListen
	}
	if config.CodexRecentHours == 0 {
		config.CodexRecentHours = defaultCodexRecentHours
	}
	config.BusyDelivery, err = deliverypolicy.Normalize(string(config.BusyDelivery))
	if err != nil {
		return Config{}, err
	}
	config.RemoteAccess = NormalizeRemoteAccess(config.RemoteAccess)
	if err := validateCodexRecentHours(config.CodexRecentHours); err != nil {
		return Config{}, err
	}
	if err := validateDashboardListen(config.Listen); err != nil {
		return Config{}, err
	}
	return config, nil
}

func Save(config Config) error {
	if config.Listen == "" {
		config.Listen = DefaultListen
	}
	if config.CodexRecentHours == 0 {
		config.CodexRecentHours = defaultCodexRecentHours
	}
	var err error
	config.BusyDelivery, err = deliverypolicy.Normalize(string(config.BusyDelivery))
	if err != nil {
		return err
	}
	config.RemoteAccess = NormalizeRemoteAccess(config.RemoteAccess)
	if err := validateCodexRecentHours(config.CodexRecentHours); err != nil {
		return err
	}
	if err := validateDashboardListen(config.Listen); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0700); err != nil {
		return fmt.Errorf("create dashboard config directory: %w", err)
	}
	return os.WriteFile(Path(), append(data, '\n'), 0600)
}

func validateCodexRecentHours(hours int) error {
	if hours < 1 || hours > maximumCodexRecentHours {
		return fmt.Errorf("Codex recent hours must be between 1 and %d", maximumCodexRecentHours)
	}
	return nil
}

// BaseURL is the dashboard's address. It carries no token: a browser
// signs in with a login link from 'agenthail dashboard'.
func BaseURL(config Config) string {
	return "http://" + config.Listen + "/"
}

func Token() (string, error) {
	data, err := os.ReadFile(TokenPath())
	if err == nil && len(data) >= 32 {
		return string(data), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read dashboard token: %w", err)
	}
	return writeNewDashboardToken()
}

// RotateToken replaces the token the local CLI and the Mac app use.
// The running daemon reads it at start, so it takes effect on restart.
func RotateToken() error {
	_, err := writeNewDashboardToken()
	return err
}

func writeNewDashboardToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate dashboard token: %w", err)
	}
	token := hex.EncodeToString(raw)
	path := TokenPath()
	temporary, err := os.CreateTemp(filepath.Dir(path), ".dashboard.token-*")
	if err != nil {
		return "", fmt.Errorf("write dashboard token: %w", err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.WriteString(token); err != nil {
		temporary.Close()
		return "", fmt.Errorf("write dashboard token: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("write dashboard token: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return "", fmt.Errorf("write dashboard token: %w", err)
	}
	return token, nil
}

func validateDashboardListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return fmt.Errorf("dashboard listen address must be host:port (got %q)", listen)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("dashboard only accepts a loopback listener; use Tailscale Serve for remote access")
	}
	return nil
}

type ClaudeSettings struct {
	DefaultRoot string `json:"defaultRoot,omitempty"`
}

func (c Config) ClaudeDefaultRoot() string {
	if c.Claude == nil {
		return ""
	}
	return c.Claude.DefaultRoot
}

func (c *Config) SetClaudeDefaultRoot(name string) {
	if name == "" {
		c.Claude = nil
		return
	}
	c.Claude = &ClaudeSettings{DefaultRoot: name}
}

const DefaultRemoteAccessPort = 7412

type RemoteAccess struct {
	Enabled       bool   `json:"enabled"`
	Provider      string `json:"provider"`
	Port          int    `json:"port"`
	TailscalePath string `json:"tailscalePath,omitempty"`
}

func NormalizeRemoteAccess(config RemoteAccess) RemoteAccess {
	if config.Provider == "" {
		config.Provider = "tailscale"
	}
	if config.Port == 0 {
		config.Port = DefaultRemoteAccessPort
	}
	return config
}

func RemoteAccessQR(value string) ([]byte, error) {
	return qrcode.Encode(value, qrcode.Medium, 512)
}
