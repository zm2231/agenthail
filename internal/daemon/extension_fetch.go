package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/extension"
)

const (
	extensionFetchPath          = "/extension/fetch"
	extensionFetchResponseLimit = 8 << 20
	extensionFetchRedirects     = 5
	extensionFetchRedacted      = "[redacted]"
)

var (
	extensionFetchTimeout        = 30 * time.Second
	extensionFetchMethods        = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	extensionFetchRefusedHeaders = []string{"Host", "Authorization", "Proxy-Authorization", "Cookie", "Connection", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length", "Forwarded", "X-Real-Ip", "Accept-Encoding", "Range", "If-Range"}
	errExtensionFetchRedirect    = errors.New("redirect leaves the mount's upstream")
	errExtensionFetchRedirects   = errors.New("too many redirects")
)

type extensionFetchRequest struct {
	Mount   string            `json:"mount"`
	Path    string            `json:"path"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type extensionFetchReply struct {
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body,omitempty"`
	BodyBase64 string            `json:"bodyBase64,omitempty"`
}

func (d *Daemon) extensionFetchHandler() http.Handler {
	return http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localReach(r) {
			writeAPIError(w, http.StatusForbidden, "remote_refused", "Extension credentials are accepted only from this machine.")
			return
		}
		id, _, ok := d.extensions.extensionByToken(bearerToken(r))
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "Only a loaded extension may fetch through a mount.")
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
			return
		}
		var request extensionFetchRequest
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid", "The fetch request is not valid JSON: "+err.Error())
			return
		}
		reply, status, code, err := d.extensionFetch(r.Context(), id, request)
		if err != nil {
			writeAPIError(w, status, code, err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, reply)
	}))
}

func (d *Daemon) extensionFetch(ctx context.Context, id string, request extensionFetchRequest) (extensionFetchReply, int, string, error) {
	method := strings.ToUpper(request.Method)
	if method == "" {
		method = http.MethodGet
	}
	if !slices.Contains(extensionFetchMethods, method) {
		return extensionFetchReply{}, http.StatusBadRequest, "invalid", fmt.Errorf("method %q is not one of %s", request.Method, strings.Join(extensionFetchMethods, ", "))
	}
	config := d.extensions.mounts.config(extension.MountKey(id, request.Mount))
	if config == nil {
		return extensionFetchReply{}, http.StatusNotFound, "not_found", fmt.Errorf("extension %s has no approved mount %q", id, request.Mount)
	}
	upstream, _ := url.Parse(config.mount.Upstream)
	origin := upstream.Scheme + "://" + upstream.Host
	target, err := mountFetchURL(origin, request.Path)
	if err != nil {
		return extensionFetchReply{}, http.StatusBadRequest, "invalid", err
	}
	ctx, cancel := context.WithTimeout(ctx, extensionFetchTimeout)
	defer cancel()
	out, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(request.Body))
	if err != nil {
		return extensionFetchReply{}, http.StatusBadRequest, "invalid", err
	}
	for name, value := range request.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if slices.Contains(extensionFetchRefusedHeaders, canonical) || strings.HasPrefix(canonical, "X-Forwarded-") {
			return extensionFetchReply{}, http.StatusBadRequest, "invalid", fmt.Errorf("header %s is set by Agenthail, not the extension", canonical)
		}
		out.Header.Set(canonical, value)
	}
	key := ""
	if config.key != nil {
		if key, err = config.readKey(ctx); err != nil {
			return extensionFetchReply{}, http.StatusBadGateway, "key_unreadable", fmt.Errorf("the key for %s could not be read: %w", config.mount.Label, err)
		}
		out.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme+"://"+next.URL.Host != origin {
			return errExtensionFetchRedirect
		}
		if len(via) >= extensionFetchRedirects {
			return errExtensionFetchRedirects
		}
		return nil
	}}
	logged := fmt.Sprintf("extension %s fetch %s %s %s", id, request.Mount, method, out.URL.Path)
	response, err := client.Do(out)
	if err != nil {
		d.log.Printf("%s: failed", logged)
		switch {
		case errors.Is(err, errExtensionFetchRedirect):
			return extensionFetchReply{}, http.StatusBadGateway, "redirect_refused", fmt.Errorf("%s redirected to another origin; only %s is reachable", config.mount.Label, origin)
		case errors.Is(err, errExtensionFetchRedirects):
			return extensionFetchReply{}, http.StatusBadGateway, "redirect_refused", fmt.Errorf("%s redirected more than %d times", config.mount.Label, extensionFetchRedirects)
		case errors.Is(err, context.DeadlineExceeded):
			return extensionFetchReply{}, http.StatusGatewayTimeout, "upstream_timeout", fmt.Errorf("%s did not answer within %s", config.mount.Label, extensionFetchTimeout)
		}
		return extensionFetchReply{}, http.StatusBadGateway, "upstream_unreachable", fmt.Errorf("%s at %s did not answer", config.mount.Label, upstream.Host)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, extensionFetchResponseLimit+1))
	if err != nil {
		d.log.Printf("%s: %d, body unreadable", logged, response.StatusCode)
		return extensionFetchReply{}, http.StatusBadGateway, "upstream_unreachable", fmt.Errorf("%s's answer could not be read", config.mount.Label)
	}
	d.log.Printf("%s: %d", logged, response.StatusCode)
	// Redaction sees only the bytes delivered, so an answer still encoded
	// after the transport's own gzip handling could carry the key hidden.
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return extensionFetchReply{}, http.StatusBadGateway, "encoded", fmt.Errorf("%s answered in a content encoding Agenthail does not pass on", config.mount.Label)
	}
	if len(body) > extensionFetchResponseLimit {
		return extensionFetchReply{}, http.StatusBadGateway, "too_large", fmt.Errorf("%s answered with more than %d bytes", config.mount.Label, extensionFetchResponseLimit)
	}
	reply := extensionFetchReply{Status: response.StatusCode, Headers: map[string]string{}}
	for name, values := range response.Header {
		if name == "Set-Cookie" {
			continue
		}
		reply.Headers[redactKey(strings.ToLower(name), strings.ToLower(key))] = redactKey(strings.Join(values, ", "), key)
	}
	if key != "" {
		body = bytes.ReplaceAll(body, []byte(key), []byte(extensionFetchRedacted))
	}
	if utf8.Valid(body) {
		reply.Body = string(body)
	} else {
		reply.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	}
	return reply, http.StatusOK, "", nil
}

// The path is joined to the upstream origin as text, so nothing in it can
// name another host.
func mountFetchURL(origin, path string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\\x00\r\n") {
		return "", fmt.Errorf("path %q must be a path on the mount's upstream, starting with a single /", path)
	}
	target, err := url.Parse(origin + path)
	if err != nil || target.Scheme+"://"+target.Host != origin || target.User != nil {
		return "", fmt.Errorf("path %q is not a path on the mount's upstream", path)
	}
	return target.String(), nil
}

func redactKey(value, key string) string {
	if key == "" {
		return value
	}
	return strings.ReplaceAll(value, key, extensionFetchRedacted)
}
