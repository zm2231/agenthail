package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/tailscale"
)

const extensionMountSyncEvery = 10 * time.Second

var (
	extensionMountPortBase  = 7420
	extensionMountPortCount = 50
	extensionTailscale      = func(config RemoteAccessConfig) (tailscale.Runner, error) {
		path, err := tailscale.Executable(config.TailscalePath)
		if err != nil {
			return nil, err
		}
		return tailscale.Command(path), nil
	}
)

type mountConfig struct {
	ext   extension.Extension
	mount extension.Mount
	key   *proxyacct.KeyRef
	proxy http.Handler

	keyMu    sync.Mutex
	keyValue string
	keyAt    time.Time
}

type servedMount struct {
	id     string
	name   string
	port   int
	server *http.Server
	cancel context.CancelFunc
	config atomic.Pointer[mountConfig]
}

// Closing the server leaves upgraded connections open; cancelling their
// context is what ends them.
func (s *servedMount) close() {
	s.cancel()
	_ = s.server.Close()
}

func (c *mountConfig) same(other *mountConfig) bool {
	return c.ext.Dir == other.ext.Dir && c.mount == other.mount && reflect.DeepEqual(c.key, other.key)
}

type mountState struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Port      int    `json:"port,omitempty"`
	Entry     string `json:"entry"`
	Upstream  string `json:"upstream"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	Published bool   `json:"published,omitempty"`
	Key       string `json:"key,omitempty"`
	Signin    bool   `json:"signin,omitempty"`
}

type extensionMounts struct {
	home    string
	syncing sync.Mutex

	mu         sync.Mutex
	served     map[string]*servedMount
	states     map[string][]mountState
	labels     map[string]string
	dnsName    string
	remotePort int
	closed     bool
}

func newExtensionMounts(home string) *extensionMounts {
	return &extensionMounts{home: home, served: map[string]*servedMount{}, states: map[string][]mountState{}}
}

func (d *Daemon) runExtensionMounts(ctx context.Context) {
	m := d.extensions.mounts
	defer m.close()
	ticker := time.NewTicker(extensionMountSyncEvery)
	defer ticker.Stop()
	for {
		if err := d.syncExtensionMounts(); err != nil && ctx.Err() == nil {
			d.log.Printf("extension mounts: %s", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *extensionMounts) close() {
	m.syncing.Lock()
	defer m.syncing.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for key, served := range m.served {
		served.close()
		delete(m.served, key)
	}
}

// A port stays assigned while the extension still declares the mount, so a
// mount waiting for approval comes back on the same port.
func (d *Daemon) syncExtensionMounts() error {
	m := d.extensions.mounts
	m.syncing.Lock()
	defer m.syncing.Unlock()
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil
	}
	found, err := extension.Discover(extension.Workspace(m.home))
	if err != nil {
		return err
	}
	ports, err := extension.ReadMountPorts(m.home)
	if err != nil {
		return fmt.Errorf("read mount ports: %w", err)
	}
	config, err := LoadDashboardConfig()
	if err != nil {
		return err
	}
	remote := normalizeRemoteAccessConfig(config.RemoteAccess)

	declared := map[string]bool{}
	keepAll := map[string]bool{}
	desired := map[string]*mountConfig{}
	states := map[string][]mountState{}
	labels := map[string]string{}
	for _, ext := range found {
		if ext.Err != nil {
			keepAll[ext.ID] = true
			continue
		}
		grant, err := d.extensions.approvals.Granted(ext.ID)
		if err != nil {
			return fmt.Errorf("read extension approvals: %w", err)
		}
		for _, mount := range ext.Manifest.Mounts {
			key := extension.MountKey(ext.ID, mount.Name)
			declared[key] = true
			state := mountState{Name: mount.Name, Label: mount.Label, Entry: mount.Entry, Upstream: mount.Upstream, State: "pending", Signin: mount.Signin != ""}
			if approved, ok := grant.MountApproved(mount); ok {
				desired[key] = &mountConfig{ext: ext, mount: mount, key: approved.Key}
				state.State = "serving"
				if approved.Key != nil {
					state.Key = approved.Key.Describe()
				}
			}
			states[ext.ID] = append(states[ext.ID], state)
			labels[ext.ID] = ext.Manifest.Label
		}
	}

	stale := map[string]bool{}
	for key := range ports {
		id, _, _ := strings.Cut(key, "/")
		if !declared[key] && !keepAll[id] {
			stale[key] = true
		}
	}

	m.mu.Lock()
	for key, served := range m.served {
		if desired[key] == nil {
			served.close()
			delete(m.served, key)
		}
	}
	errs := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(desired)) {
		config := desired[key]
		served := m.served[key]
		if served != nil && served.config.Load().same(config) {
			continue
		}
		if served == nil {
			var err error
			if served, err = d.listenMount(key, ports); err != nil {
				errs[key] = err.Error()
				continue
			}
			m.served[key] = served
		}
		config.proxy = d.mountProxy(config)
		served.config.Store(config)
	}
	needName := remote.Enabled && len(m.served) > 0 && m.dnsName == ""
	m.mu.Unlock()

	dnsName, publishErrs := d.publishMounts(remote, ports, needName)
	for key, message := range publishErrs {
		if errs[key] == "" {
			errs[key] = message
		}
	}
	for key := range stale {
		if !ports[key].Published {
			delete(ports, key)
		}
	}
	writeErr := extension.WriteMountPorts(m.home, ports)

	m.mu.Lock()
	for id, list := range states {
		for i := range list {
			key := extension.MountKey(id, list[i].Name)
			list[i].Port = ports[key].Port
			list[i].Published = ports[key].Published
			if message := errs[key]; message != "" {
				list[i].State, list[i].Error = "failed", message
			}
		}
	}
	m.states, m.labels = states, labels
	m.remotePort = remote.Port
	if !remote.Enabled {
		m.dnsName = ""
	} else if dnsName != "" {
		m.dnsName = dnsName
	}
	m.mu.Unlock()
	if writeErr != nil {
		return fmt.Errorf("record mount ports: %w", writeErr)
	}
	d.invalidateDashboard()
	return nil
}

func (d *Daemon) listenMount(key string, ports map[string]extension.MountPort) (*servedMount, error) {
	id, name, _ := strings.Cut(key, "/")
	var listener net.Listener
	assigned, ok := ports[key]
	if ok {
		var err error
		if listener, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(assigned.Port))); err != nil {
			return nil, fmt.Errorf("port %d: %w", assigned.Port, err)
		}
	} else {
		var err error
		if listener, err = listenFreeMountPort(ports); err != nil {
			return nil, err
		}
		assigned = extension.MountPort{Port: listener.Addr().(*net.TCPAddr).Port}
		ports[key] = assigned
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := &servedMount{id: id, name: name, port: assigned.Port, cancel: cancel}
	served.server = &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler:           d.mountHandler(served),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          d.log,
	}
	go func() {
		if err := served.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.log.Printf("extension %s: mount %s: %s", id, name, err)
		}
	}()
	return served, nil
}

func listenFreeMountPort(ports map[string]extension.MountPort) (net.Listener, error) {
	if extensionMountPortBase == 0 {
		return net.Listen("tcp", "127.0.0.1:0")
	}
	taken := map[int]bool{}
	for _, assigned := range ports {
		taken[assigned.Port] = true
	}
	for port := extensionMountPortBase; port < extensionMountPortBase+extensionMountPortCount; port++ {
		if taken[port] {
			continue
		}
		if listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			return listener, nil
		}
	}
	return nil, fmt.Errorf("no free port from %d to %d for the mount", extensionMountPortBase, extensionMountPortBase+extensionMountPortCount-1)
}

// A mount that is not served, or any mount while remote access is off, has
// its route removed; a removed mount keeps its port until that succeeds.
func (d *Daemon) publishMounts(remote RemoteAccessConfig, ports map[string]extension.MountPort, needName bool) (string, map[string]string) {
	errs := map[string]string{}
	var work []string
	for key, assigned := range ports {
		served := d.extensions.mounts.serving(key)
		if (remote.Enabled && served && !assigned.Published) || (assigned.Published && (!remote.Enabled || !served)) {
			work = append(work, key)
		}
	}
	if len(work) == 0 && !needName {
		return "", errs
	}
	fail := func(err error) (string, map[string]string) {
		for _, key := range work {
			errs[key] = "Tailscale: " + err.Error()
		}
		return "", errs
	}
	run, err := extensionTailscale(remote)
	if err != nil {
		return fail(err)
	}
	node, err := tailscale.ReadNode(run)
	if err != nil {
		return fail(err)
	}
	for _, key := range slices.Sorted(slices.Values(work)) {
		assigned := ports[key]
		if !assigned.Published {
			if err := tailscale.Publish(run, node.Self.DNSName, mountTarget(assigned.Port), assigned.Port); err != nil {
				errs[key] = "Tailscale: " + err.Error()
				continue
			}
			assigned.Published = true
		} else {
			if _, err := tailscale.Unpublish(run, node.Self.DNSName, mountTarget(assigned.Port), assigned.Port); err != nil {
				errs[key] = "Tailscale: " + err.Error()
				continue
			}
			assigned.Published = false
		}
		ports[key] = assigned
	}
	return node.Self.DNSName, errs
}

func mountTarget(port int) string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func (m *extensionMounts) serving(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.served[key] != nil
}

func (m *extensionMounts) view() (map[string][]mountState, map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := map[string][]mountState{}
	for id, list := range m.states {
		states[id] = slices.Clone(list)
	}
	return states, maps.Clone(m.labels)
}

// A page on another site that rebinds its hostname to 127.0.0.1 is refused
// here, before any credential is checked.
func (m *extensionMounts) allowedHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dnsName != "" && host == strings.ToLower(m.dnsName)
}

func (c *mountConfig) readKey(ctx context.Context) (string, error) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	if c.keyValue != "" && time.Since(c.keyAt) < time.Minute {
		return c.keyValue, nil
	}
	value, err := c.key.Read(ctx)
	if err != nil {
		return "", err
	}
	c.keyValue, c.keyAt = value, time.Now()
	return value, nil
}
