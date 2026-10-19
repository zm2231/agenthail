package hosts

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	ProtocolVersion = 1
	// FederationVersion names what one daemon serves another beyond the
	// client API: the session-options launch catalog and history filtered
	// by session. A host outside this range is reported as needing an update.
	FederationVersion = 2
	ResponseHeader    = "Agenthail-Host"
	responseBodyLimit = 32 << 20
)

// TailnetPrefixes are the addresses Tailscale assigns. A paired host is only
// ever dialed inside them.
var TailnetPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// CheckTailnetAddress accepts an IP inside TailnetPrefixes and nothing else.
func CheckTailnetAddress(address string) error {
	ip, err := netip.ParseAddr(address)
	if err != nil || ip.Zone() != "" {
		return fmt.Errorf("host address must be a tailnet IP")
	}
	ip = ip.Unmap()
	for _, prefix := range TailnetPrefixes {
		if prefix.Contains(ip) {
			return nil
		}
	}
	return fmt.Errorf("host address %s is outside the tailnet ranges", address)
}

// NotSentError means the request never reached the remote host: the dial or
// TLS handshake failed before any request byte was written. Only this error
// proves a write was not delivered.
type NotSentError struct{ Err error }

func (e NotSentError) Error() string { return e.Err.Error() }
func (e NotSentError) Unwrap() error { return e.Err }

// UnansweredError is a response written by something in front of the daemon,
// such as Tailscale Serve answering 502 while the daemon is down. The request
// reached that intermediary, so whether the daemon applied it is unknown.
type UnansweredError struct {
	Endpoint string
	Status   int
}

func (e UnansweredError) Error() string {
	return fmt.Sprintf("%s answered HTTP %d, but no Agenthail daemon answered behind it", e.Endpoint, e.Status)
}

type StatusError struct {
	Status  int
	Code    string
	Message string
}

func (e StatusError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("remote host returned HTTP %d", e.Status)
}

// Client talks to a paired daemon over the tailnet. It dials Address and
// verifies TLS against the endpoint's DNS name, so the remote's Tailscale
// Serve certificate is valid without resolving MagicDNS.
type Client struct {
	endpoint *url.URL
	token    string
	http     *http.Client
	stream   *http.Client
}

type ClientOptions struct {
	RootCAs *x509.CertPool
}

func NewClient(endpoint, address, token string, options ClientOptions) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("host endpoint must be an https URL")
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	if err := CheckTailnetAddress(address); err != nil {
		return nil, err
	}
	target := net.JoinHostPort(address, port)
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, target)
		},
		TLSClientConfig:     &tls.Config{ServerName: parsed.Hostname(), RootCAs: options.RootCAs, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 4,
	}
	base := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}
	return &Client{endpoint: base, token: token, http: &http.Client{Transport: transport, Timeout: 30 * time.Second}, stream: &http.Client{Transport: transport}}, nil
}

func (c *Client) Endpoint() string { return c.endpoint.String() }

func (c *Client) url(path string, query url.Values) string {
	target := *c.endpoint
	target.Path = path
	target.RawQuery = query.Encode()
	return target.String()
}

// Do sends one request. A failure before any byte was written is a
// NotSentError; any later failure leaves the outcome unknown.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body []byte, header http.Header) (*http.Response, error) {
	return c.do(ctx, c.http, method, path, query, body, header)
}

// Open is Do without a response deadline, for event streams.
func (c *Client) Open(ctx context.Context, path string, query url.Values, header http.Header) (*http.Response, error) {
	return c.do(ctx, c.stream, http.MethodGet, path, query, nil, header)
}

func (c *Client) do(ctx context.Context, client *http.Client, method, path string, query url.Values, body []byte, header http.Header) (*http.Response, error) {
	var wrote atomic.Bool
	trace := &httptrace.ClientTrace{WroteHeaderField: func(string, []string) { wrote.Store(true) }}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, c.url(path, query), reader)
	if err != nil {
		return nil, NotSentError{Err: err}
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if body != nil && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := client.Do(request)
	if err != nil {
		err = fmt.Errorf("reach host %s: %w", c.endpoint.Host, scrub(err, c.token))
		if !wrote.Load() {
			return nil, NotSentError{Err: err}
		}
		return nil, err
	}
	if response.Header.Get(ResponseHeader) == "" {
		response.Body.Close()
		return nil, UnansweredError{Endpoint: c.endpoint.Host, Status: response.StatusCode}
	}
	return response, nil
}

func scrub(err error, token string) error {
	if token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "[token]"))
}

// ReadResponse returns the body of a 2xx response, or a StatusError carrying
// the remote's API error.
func ReadResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, responseBodyLimit))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return body, nil
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	statusErr := StatusError{Status: response.StatusCode}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		statusErr.Code, statusErr.Message = envelope.Error.Code, envelope.Error.Message
	} else {
		statusErr.Message = strings.TrimSpace(string(body))
	}
	return nil, statusErr
}

func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, value any) error {
	response, err := c.Do(ctx, http.MethodGet, path, query, nil, nil)
	if err != nil {
		return err
	}
	body, err := ReadResponse(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, value)
}

type Version struct {
	Protocol          int    `json:"protocol"`
	MinimumProtocol   int    `json:"minimumProtocol"`
	MaximumProtocol   int    `json:"maximumProtocol"`
	MinimumFederation int    `json:"minimumFederation"`
	MaximumFederation int    `json:"maximumFederation"`
	HostID            string `json:"hostId"`
	HostName          string `json:"hostName"`
}

func (c *Client) Version(ctx context.Context) (Version, error) {
	var version Version
	if err := c.GetJSON(ctx, "/api/v1/version", nil, &version); err != nil {
		return Version{}, err
	}
	if version.MinimumProtocol > ProtocolVersion || version.MaximumProtocol < ProtocolVersion || version.MinimumFederation > FederationVersion || version.MaximumFederation < FederationVersion {
		return version, ErrProtocolMismatch{Remote: version}
	}
	return version, nil
}

type ErrProtocolMismatch struct{ Remote Version }

func (e ErrProtocolMismatch) Error() string {
	if e.Remote.MinimumProtocol > ProtocolVersion || e.Remote.MaximumProtocol < ProtocolVersion {
		return fmt.Sprintf("host speaks protocol %d to %d; this daemon speaks %d", e.Remote.MinimumProtocol, e.Remote.MaximumProtocol, ProtocolVersion)
	}
	if e.Remote.MaximumFederation < FederationVersion {
		return fmt.Sprintf("host speaks federation %d to %d; this daemon speaks %d; update Agenthail on that host", e.Remote.MinimumFederation, e.Remote.MaximumFederation, FederationVersion)
	}
	return fmt.Sprintf("host speaks federation %d to %d; this daemon speaks %d; update Agenthail on this machine", e.Remote.MinimumFederation, e.Remote.MaximumFederation, FederationVersion)
}

type PairingLink struct {
	Endpoint string
	Secret   string
}

func ParsePairingLink(link string) (PairingLink, error) {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil || parsed.Scheme != "agenthail" || parsed.Host != "pair" {
		return PairingLink{}, fmt.Errorf("pairing link must be an agenthail://pair link from 'agenthail hosts invite'")
	}
	endpoint, secret := parsed.Query().Get("endpoint"), parsed.Query().Get("secret")
	if endpoint == "" || secret == "" {
		return PairingLink{}, fmt.Errorf("pairing link is missing its endpoint or secret")
	}
	return PairingLink{Endpoint: strings.TrimRight(endpoint, "/"), Secret: secret}, nil
}

// Pair redeems a pairing secret on the remote and returns the device token.
func Pair(ctx context.Context, link PairingLink, address, deviceName string, options ClientOptions) (string, Version, error) {
	anonymous, err := NewClient(link.Endpoint, address, "", options)
	if err != nil {
		return "", Version{}, err
	}
	payload, _ := json.Marshal(map[string]string{"secret": link.Secret, "name": deviceName})
	response, err := anonymous.Do(ctx, http.MethodPost, "/api/v1/pair", nil, payload, nil)
	if err != nil {
		return "", Version{}, err
	}
	body, err := ReadResponse(response)
	if err != nil {
		return "", Version{}, err
	}
	var completed struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &completed); err != nil || completed.Token == "" {
		return "", Version{}, fmt.Errorf("remote host did not return a device token")
	}
	paired, err := NewClient(link.Endpoint, address, completed.Token, options)
	if err != nil {
		return "", Version{}, err
	}
	version, err := paired.Version(ctx)
	if err != nil {
		revokeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if response, revokeErr := paired.Do(revokeCtx, http.MethodDelete, "/api/v1/device", nil, nil, nil); revokeErr == nil {
			response.Body.Close()
		}
		return "", version, err
	}
	return completed.Token, version, nil
}
