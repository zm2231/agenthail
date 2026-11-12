package daemon

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	extensionKeyPlaceholder = "agenthail-key"
	extensionSigninPath     = "/_agenthail/signin.js"
	extensionInjectLimit    = 8 << 20
)

var htmlHead = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)

type mountRequestKey struct{}

type mountRequest struct {
	dropAuthorization bool
	key               string
	inject            bool
	frameAncestors    string
}

func (d *Daemon) mountHandler(served *servedMount) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.extensions.mounts.allowedHost(r.Host) {
			writeAPIError(w, http.StatusMisdirectedRequest, "misdirected", "This port answers only for this machine's own names.")
			return
		}
		dashboard := d.dashboard.Load()
		if dashboard == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "The dashboard is off, so mounts cannot check who is asking.")
			return
		}
		scope := "control"
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			scope = "read"
		}
		d.apiV1Guard(dashboard, scope, func(w http.ResponseWriter, r *http.Request) {
			d.serveMount(w, r, served, dashboard)
		})(w, r)
	})
}

func (d *Daemon) serveMount(w http.ResponseWriter, r *http.Request, served *servedMount, dashboard *dashboardServer) {
	config := served.config.Load()
	if r.URL.Path == extensionSigninPath {
		serveMountSignin(w, r, config)
		return
	}
	if r.Header.Get("Upgrade") != "" && !sameOrigin(r) {
		writeAPIError(w, http.StatusForbidden, "cross_origin", "Only the mount's own pages may open a connection to it.")
		return
	}
	shell := d.shellOrigins(r, dashboard)
	request := mountRequest{
		dropAuthorization: dashboardBearer(r.Context()) || r.Context().Value(apiDeviceTokenContextKey{}) != nil,
		frameAncestors:    strings.Join(shell, " "),
	}
	if config.key != nil && r.Header.Get("Sec-Fetch-Site") == "same-origin" && r.Header.Get("Authorization") == "Bearer "+extensionKeyPlaceholder {
		key, err := config.readKey(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "key_unreadable", "The key for "+config.mount.Label+" could not be read: "+err.Error())
			return
		}
		request.key = key
	}
	request.inject = config.key != nil && config.mount.Signin != "" && navigation(r) && launchedBy(r, shell)
	config.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mountRequestKey{}, request)))
}

func serveMountSignin(w http.ResponseWriter, r *http.Request, config *mountConfig) {
	file, ok := "", false
	if config.key != nil && config.mount.Signin != "" {
		file, ok = config.ext.Asset(strings.TrimPrefix(config.mount.Signin, "/"))
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "This mount has no sign-in script.")
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "The sign-in script could not be read.")
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// shellOrigins are the dashboard origins on the host the browser used: the
// only pages that may frame a mount.
func (d *Daemon) shellOrigins(r *http.Request, dashboard *dashboardServer) []string {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	origins := []string{}
	if _, port, err := net.SplitHostPort(dashboard.listen); err == nil {
		origins = append(origins, "http://"+net.JoinHostPort(host, port))
	}
	d.extensions.mounts.mu.Lock()
	remotePort := d.extensions.mounts.remotePort
	d.extensions.mounts.mu.Unlock()
	if remotePort != 0 {
		origins = append(origins, "https://"+net.JoinHostPort(host, strconv.Itoa(remotePort)))
	}
	return origins
}

func navigation(r *http.Request) bool {
	dest := r.Header.Get("Sec-Fetch-Dest")
	return r.Method == http.MethodGet && (dest == "document" || dest == "iframe")
}

func launchedBy(r *http.Request, shell []string) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "none", "same-origin":
		return true
	case "same-site":
		referer, err := url.Parse(r.Header.Get("Referer"))
		if err != nil || referer.Host == "" {
			return false
		}
		origin := referer.Scheme + "://" + referer.Host
		for _, allowed := range shell {
			if origin == allowed {
				return true
			}
		}
	}
	return false
}

func mountCookiePrefix(id, name string) string { return "ah_" + id + "_" + name + "_" }

func (d *Daemon) mountProxy(config *mountConfig) http.Handler {
	upstream, _ := url.Parse(config.mount.Upstream)
	origin := upstream.Scheme + "://" + upstream.Host
	jar := mountCookiePrefix(config.ext.ID, config.mount.Name)
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			request, _ := pr.In.Context().Value(mountRequestKey{}).(mountRequest)
			pr.SetURL(upstream)
			out := pr.Out.Header
			out.Del("Referer")
			if out.Get("Origin") != "" {
				out.Set("Origin", origin)
			}
			out.Del("Cookie")
			var kept []string
			for _, cookie := range pr.In.Cookies() {
				if name, ok := strings.CutPrefix(cookie.Name, jar); ok && name != "" {
					kept = append(kept, name+"="+cookie.Value)
				}
			}
			if len(kept) > 0 {
				out.Set("Cookie", strings.Join(kept, "; "))
			}
			if request.dropAuthorization {
				out.Del("Authorization")
			}
			if request.key != "" {
				out.Set("Authorization", "Bearer "+request.key)
			}
			if request.inject {
				out.Set("Accept-Encoding", "identity")
			}
		},
		FlushInterval: -1,
		ErrorLog:      d.log,
		ModifyResponse: func(resp *http.Response) error {
			request, _ := resp.Request.Context().Value(mountRequestKey{}).(mountRequest)
			rewriteMountHeaders(resp.Header, origin, jar, request.frameAncestors)
			if request.inject && resp.StatusCode == http.StatusOK {
				return injectSignin(resp)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeAPIError(w, http.StatusBadGateway, "upstream_unreachable", config.mount.Label+" at "+upstream.Host+" did not answer: "+err.Error())
		},
	}
}

func rewriteMountHeaders(header http.Header, origin, jar, frameAncestors string) {
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			header.Del(name)
		}
	}
	header.Del("X-Frame-Options")
	policies := header.Values("Content-Security-Policy")
	header.Del("Content-Security-Policy")
	for _, policy := range policies {
		var kept []string
		for _, directive := range strings.Split(policy, ";") {
			if directive = strings.TrimSpace(directive); directive != "" && !strings.HasPrefix(strings.ToLower(directive), "frame-ancestors") {
				kept = append(kept, directive)
			}
		}
		if len(kept) > 0 {
			header.Add("Content-Security-Policy", strings.Join(kept, "; "))
		}
	}
	header.Add("Content-Security-Policy", "frame-ancestors 'self' "+frameAncestors)
	if location := header.Get("Location"); location != "" {
		if target, err := url.Parse(location); err == nil && target.IsAbs() && target.Scheme+"://"+target.Host == origin {
			target.Scheme, target.Host, target.User = "", "", nil
			header.Set("Location", target.String())
		}
	}
	cookies := header.Values("Set-Cookie")
	header.Del("Set-Cookie")
	for _, cookie := range cookies {
		parts := strings.Split(cookie, ";")
		kept := []string{jar + strings.TrimSpace(parts[0])}
		for _, attribute := range parts[1:] {
			if attribute = strings.TrimSpace(attribute); attribute != "" && !strings.HasPrefix(strings.ToLower(attribute), "domain=") {
				kept = append(kept, attribute)
			}
		}
		header.Add("Set-Cookie", strings.Join(kept, "; "))
	}
}

func injectSignin(resp *http.Response) error {
	kind, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || kind != "text/html" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return nil
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, extensionInjectLimit+1))
	if err != nil {
		return err
	}
	if len(body) > extensionInjectLimit || !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
		return nil
	}
	_ = resp.Body.Close()
	tag := []byte(`<script src="` + extensionSigninPath + `"></script>`)
	if at := htmlHead.FindIndex(body); at != nil {
		body = append(body[:at[1]:at[1]], append(tag, body[at[1]:]...)...)
	} else {
		body = append(tag, body...)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	return nil
}
