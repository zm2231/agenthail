package tailscale

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

var ErrNotInstalled = errors.New("the Tailscale CLI is not installed")

type Runner func(args ...string) ([]byte, error)

type Node struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		DNSName string `json:"DNSName"`
		Online  bool   `json:"Online"`
	} `json:"Self"`
}

type Serve struct {
	AllowFunnel map[string]bool `json:"AllowFunnel"`
	TCP         map[string]struct {
		HTTP bool `json:"HTTP"`
	} `json:"TCP"`
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

func Executable(explicit string) (string, error) {
	candidates := []string{explicit, os.Getenv("AGENTHAIL_TAILSCALE")}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	if path, err := exec.LookPath("tailscale"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			resolved = candidate
		}
		if info, err := os.Stat(resolved); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return resolved, nil
		}
	}
	return "", ErrNotInstalled
}

func Command(path string) Runner {
	return func(args ...string) ([]byte, error) {
		command := exec.Command(path, args...)
		command.Env = append(os.Environ(), "SHELL=/bin/zsh", "TERM=xterm-256color")
		output, err := command.CombinedOutput()
		if err != nil {
			return output, fmt.Errorf("tailscale %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
		return output, nil
	}
}

func ReadNode(run Runner) (Node, error) {
	output, err := run("status", "--json")
	if err != nil {
		return Node{}, err
	}
	var node Node
	if err := json.Unmarshal(output, &node); err != nil {
		return Node{}, fmt.Errorf("parse Tailscale status: %w", err)
	}
	node.Self.DNSName = strings.TrimSuffix(node.Self.DNSName, ".")
	if node.Self.DNSName == "" {
		return Node{}, fmt.Errorf("this computer has no MagicDNS name; enable MagicDNS for the tailnet")
	}
	if !node.Self.Online && node.BackendState != "Running" {
		return Node{}, fmt.Errorf("the Tailscale CLI is installed but offline; connect Tailscale first")
	}
	return node, nil
}

func ReadServe(run Runner) (Serve, error) {
	output, err := run("serve", "status", "--json")
	if err != nil {
		return Serve{}, err
	}
	var serve Serve
	if err := json.Unmarshal(output, &serve); err != nil {
		return Serve{}, fmt.Errorf("parse Tailscale Serve status: %w", err)
	}
	return serve, nil
}

func Host(dnsName string, port int) string {
	return net.JoinHostPort(dnsName, strconv.Itoa(port))
}

func URL(dnsName string, port int) string {
	return (&url.URL{Scheme: "https", Host: Host(dnsName, port), Path: "/"}).String()
}

func (s Serve) Proxy(dnsName string, port int) string {
	return s.Web[Host(dnsName, port)].Handlers["/"].Proxy
}

func (s Serve) Funnel(dnsName string, port int) bool {
	return s.AllowFunnel[Host(dnsName, port)]
}

func (s Serve) PortUsed(port int) bool {
	_, used := s.TCP[strconv.Itoa(port)]
	return used
}

func (s Serve) Routes(dnsName, target string, port int, https bool) bool {
	return target != "" && s.Proxy(dnsName, port) == target && https && !s.Funnel(dnsName, port)
}

func ServesHTTPS(run Runner, dnsName string, port int) (bool, error) {
	output, err := run("serve", "status")
	if err != nil {
		return false, err
	}
	host := dnsName
	if port != 443 {
		host = Host(dnsName, port)
	}
	return strings.Contains(string(output), "https://"+host), nil
}

func Target(listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", fmt.Errorf("dashboard listen address must be host:port")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port), nil
}

func Publish(run Runner, dnsName, target string, port int) error {
	serve, err := ReadServe(run)
	if err != nil {
		return err
	}
	portKey := strconv.Itoa(port)
	proxy := serve.Proxy(dnsName, port)
	httpsReady, err := ServesHTTPS(run, dnsName, port)
	if err != nil {
		return err
	}
	if serve.Funnel(dnsName, port) {
		return fmt.Errorf("port %d is open to Tailscale Funnel; disable Funnel first", port)
	}
	if serve.PortUsed(port) && proxy != "" && proxy != target {
		return fmt.Errorf("port %d already proxies %s in Tailscale Serve", port, proxy)
	}
	if serve.PortUsed(port) && proxy == "" {
		return fmt.Errorf("port %d is already used by another Tailscale Serve route", port)
	}
	ownedHTTP := proxy == target && !httpsReady
	if ownedHTTP {
		if _, err := run("serve", "--http="+portKey, "off"); err != nil {
			return err
		}
	}
	if proxy != target || !httpsReady {
		if _, err := run("serve", "--bg", "--yes", "--https="+portKey, target); err != nil {
			if ownedHTTP {
				_, _ = run("serve", "--bg", "--yes", "--http="+portKey, target)
			}
			return err
		}
	}
	serve, err = ReadServe(run)
	if err != nil {
		return err
	}
	if serve.Proxy(dnsName, port) != target {
		return fmt.Errorf("the Agenthail proxy on port %d did not stay in Tailscale Serve", port)
	}
	if httpsReady, err := ServesHTTPS(run, dnsName, port); err != nil || !httpsReady {
		if err != nil {
			return err
		}
		return fmt.Errorf("HTTPS did not turn on for port %d in Tailscale Serve", port)
	}
	if serve.Funnel(dnsName, port) {
		return fmt.Errorf("port %d became open to Tailscale Funnel; sharing was not accepted", port)
	}
	return nil
}

func Unpublish(run Runner, dnsName, target string, port int) (bool, error) {
	serve, err := ReadServe(run)
	if err != nil {
		return false, err
	}
	proxy := serve.Proxy(dnsName, port)
	if proxy == "" {
		return false, nil
	}
	if proxy != target {
		return false, fmt.Errorf("refusing to remove Tailscale Serve port %d because it proxies %s", port, proxy)
	}
	httpsReady, err := ServesHTTPS(run, dnsName, port)
	if err != nil {
		return false, err
	}
	protocol := "--http="
	if httpsReady {
		protocol = "--https="
	}
	if _, err := run("serve", protocol+strconv.Itoa(port), "off"); err != nil {
		return false, err
	}
	return true, nil
}
