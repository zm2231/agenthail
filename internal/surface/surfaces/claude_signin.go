package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

type claudeWebAccount struct {
	UUID  string `json:"uuid"`
	Email string `json:"email_address"`
}

// claudeWebAccounts holds each profile's account for cookieHeaderTTL, so a
// profile that signs into another account is seen within that time.
type claudeWebAccounts struct {
	mu        sync.Mutex
	byProfile map[string]claudeWebAccountEntry
}

type claudeWebAccountEntry struct {
	account   claudeWebAccount
	expiresAt time.Time
}

// cookieSource is the sign-in profile signed into the account of sess's
// config root: Claude drops every Remote Control bridge when its root signs
// into another account, so a bridge always belongs to the root's current
// account. Without a session, or for a root with no claude.ai login, it is
// the consumer's choice. It runs before anything is posted, so a missing
// profile never becomes an uncertain delivery.
func (c *Claude) cookieSource(ctx context.Context, sess *surface.Session) (CookieSource, error) {
	root, ok, err := c.sessionConfigRoot(sess)
	if err != nil {
		if surface.IsDeliveryTerminal(err) {
			return CookieSource{}, err
		}
		return CookieSource{}, surface.DeliveryUnavailable(err)
	}
	if ok {
		return c.accountCookies(ctx, root)
	}
	source, err := signin.Resolve(c.signins, signin.Target{Consumer: signin.Claude})
	if err != nil {
		return CookieSource{}, err
	}
	return c.cookies(source), nil
}

// cookieSourceError keeps a failure that already says how delivery failed and
// reports any other as the sign-in problem it is.
func cookieSourceError(err error) error {
	if surface.IsDeliveryTerminal(err) || surface.IsDeliveryUnavailable(err) {
		return err
	}
	return surface.DeliveryTerminal(err, signinTerminalKind(err))
}

func (c *Claude) cookies(source signin.Source) CookieSource {
	return CookieSource{Bridge: c.cookieBridge, URL: "https://claude.ai/", Signin: source}
}

// sessionConfigRoot is the signed-in config root holding the
// session: its transcript's root, or, before a live process has written its
// transcript, the root holding its records. Send and Control refuse a
// conversation held by two roots before any request is made.
func (c *Claude) sessionConfigRoot(sess *surface.Session) (surface.ClaudeConfigRoot, bool, error) {
	if sess == nil || sess.ID == "" {
		return surface.ClaudeConfigRoot{}, false, nil
	}
	path := ""
	if sess.Transcript != "" {
		path = claudeTranscriptRoot(sess.Transcript)
	} else {
		var err error
		if path, err = c.conversationRoot(sess.ID); err != nil {
			return surface.ClaudeConfigRoot{}, false, err
		}
	}
	if path == "" {
		return surface.ClaudeConfigRoot{}, false, nil
	}
	roots, err := surface.DescribeClaudeConfigRoots(c.home)
	if err != nil {
		return surface.ClaudeConfigRoot{}, false, err
	}
	root, ok := surface.ClaudeConfigRootFor(roots, path)
	if ok && root.Error != "" {
		return surface.ClaudeConfigRoot{}, false, fmt.Errorf("read the login of %s: %s", root.Root, root.Error)
	}
	return root, ok && root.SignedInAs != nil && root.SignedInAs.AccountID != "", nil
}

// accountCookies is the profile signed into root's login. It refuses only when
// every profile answered for another account or none; a profile that could not
// be asked leaves the answer open, so the message is held.
func (c *Claude) accountCookies(ctx context.Context, root surface.ClaudeConfigRoot) (CookieSource, error) {
	candidates, err := signin.Candidates(c.signins, signin.Claude)
	if err != nil {
		return CookieSource{}, err
	}
	login := root.SignedInAs
	var seen []string
	answered := true
	for _, candidate := range candidates {
		source := c.cookies(candidate)
		web, err := c.webAccount(ctx, source)
		var status claudeHTTPStatusError
		switch {
		case errors.As(err, &status) && status.API && (status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden):
			seen = append(seen, fmt.Sprintf("%s is not signed into claude.ai", signinLabel(candidate)))
		case err != nil:
			answered = false
			seen = append(seen, fmt.Sprintf("%s: %v", signinLabel(candidate), err))
		case web.UUID == login.AccountID:
			return source, nil
		default:
			seen = append(seen, fmt.Sprintf("%s is %s", signinLabel(candidate), web.Email))
		}
	}
	if !answered {
		return CookieSource{}, surface.DeliveryUnavailable(fmt.Errorf("could not confirm which sign-in profile is signed into claude.ai as %s, the account of %s (%s)", login.Email, root.Root, strings.Join(seen, "; ")))
	}
	return CookieSource{}, signin.ProblemError{Consumer: signin.Claude, Err: fmt.Errorf("no sign-in profile is signed into claude.ai as %s, the account of %s (%s); sign in to claude.ai as %s in a browser profile and add it with `agenthail signin add`", login.Email, root.Root, strings.Join(seen, "; "), login.Email)}
}

func signinLabel(source signin.Source) string {
	switch {
	case source.ProfileID != "":
		return source.Label
	case source.Label != "":
		return source.Browser.Label() + " " + source.Label
	}
	return source.Browser.Label()
}

func (c *Claude) webAccount(ctx context.Context, source CookieSource) (claudeWebAccount, error) {
	key := source.cacheKey()
	c.webAccounts.mu.Lock()
	entry, ok := c.webAccounts.byProfile[key]
	c.webAccounts.mu.Unlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.account, nil
	}
	data, err := c.claudeGET(ctx, source, "/api/account", "account")
	if err != nil {
		return claudeWebAccount{}, err
	}
	var account claudeWebAccount
	if err := json.Unmarshal([]byte(data), &account); err != nil || account.UUID == "" {
		return claudeWebAccount{}, fmt.Errorf("claude.ai did not name the signed-in account")
	}
	c.webAccounts.mu.Lock()
	c.webAccounts.byProfile[key] = claudeWebAccountEntry{account: account, expiresAt: time.Now().Add(cookieHeaderTTL)}
	c.webAccounts.mu.Unlock()
	return account, nil
}

// claudeGET reads a claude.ai API path; name stands for the path in errors.
func (c *Claude) claudeGET(ctx context.Context, source CookieSource, path, name string) (string, error) {
	request := c.request
	if request == nil {
		request = claudeSendRequest
	}
	status, data, err := request(ctx, "GET", "https://claude.ai"+path, c.headerMap("", ""), "", source, 15*time.Second)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK || strings.Contains(data, "Just a moment") {
		return "", claudeHTTPStatusError{Name: name, Status: status, API: strings.HasPrefix(strings.TrimSpace(data), "{")}
	}
	return data, nil
}

// claudeHTTPStatusError is a claude.ai answer other than 200. API is true when
// claude.ai's API answered, not a Cloudflare page in front of it.
type claudeHTTPStatusError struct {
	Name   string
	Status int
	API    bool
}

func (e claudeHTTPStatusError) Error() string {
	if !e.API {
		return fmt.Sprintf("claude.ai %s answered with a Cloudflare page (HTTP %d)", e.Name, e.Status)
	}
	return fmt.Sprintf("claude.ai %s returned HTTP %d", e.Name, e.Status)
}

// ClaudeConfigRoots describes each config root with the sign-in profile
// signed into its login and that login's usage as claude.ai reports it now.
func (c *Claude) ClaudeConfigRoots(ctx context.Context) ([]surface.ClaudeConfigRoot, error) {
	roots, err := surface.DescribeClaudeConfigRoots(c.home)
	if err != nil {
		return nil, err
	}
	for i := range roots {
		c.describeLogin(ctx, &roots[i])
	}
	return roots, nil
}

// ClaudeConfigRoot describes the one config root at path, as ClaudeConfigRoots
// does.
func (c *Claude) ClaudeConfigRoot(ctx context.Context, path string) (surface.ClaudeConfigRoot, error) {
	roots, err := surface.DescribeClaudeConfigRoots(c.home)
	if err != nil {
		return surface.ClaudeConfigRoot{}, err
	}
	root, ok := surface.ClaudeConfigRootFor(roots, path)
	if !ok {
		return surface.ClaudeConfigRoot{}, fmt.Errorf("%s is not a configured Claude config root", path)
	}
	c.describeLogin(ctx, &root)
	return root, nil
}

func (c *Claude) describeLogin(ctx context.Context, root *surface.ClaudeConfigRoot) {
	if root.SignedInAs == nil || root.SignedInAs.AccountID == "" {
		return
	}
	if c.localOnly {
		root.UsageProblem = "usage is read from claude.ai with a browser sign-in, and this install has no claude.ai cookie reader"
		return
	}
	source, err := c.accountCookies(ctx, *root)
	if err != nil {
		root.SigninProblem = err.Error()
		switch {
		case signin.IsOff(err):
			root.UsageProblem = "browser sign-in is off for Claude, so usage is not read"
		case signin.IsUnavailable(err):
			root.UsageProblem = "this computer has no browser to read the claude.ai sign-in from, so usage is not read"
		case surface.IsDeliveryUnavailable(err):
			root.UsageProblem = "claude.ai could not be asked which profile holds this login; usage is shown once it answers"
		}
		return
	}
	root.Signin = signinLabel(source.Signin)
	if root.Usage, err = c.claudeUsage(ctx, source, root.SignedInAs.OrganizationID); err != nil {
		root.UsageProblem = err.Error()
	}
}

type claudeUsageResponse struct {
	Limits []struct {
		Kind     string  `json:"kind"`
		Group    string  `json:"group"`
		Percent  float64 `json:"percent"`
		Severity string  `json:"severity"`
		ResetsAt string  `json:"resets_at"`
		Scope    *struct {
			Model *struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	} `json:"limits"`
}

func (c *Claude) claudeUsage(ctx context.Context, source CookieSource, organization string) (*surface.Usage, error) {
	if organization == "" {
		return nil, fmt.Errorf("the config root does not name its organization")
	}
	data, err := c.claudeGET(ctx, source, "/api/organizations/"+organization+"/usage", "usage")
	if err != nil {
		return nil, errors.New(strings.ReplaceAll(err.Error(), organization, "<organization>"))
	}
	var response claudeUsageResponse
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return nil, fmt.Errorf("parse claude.ai usage: %w", err)
	}
	usage := &surface.Usage{FetchedAt: time.Now().UTC()}
	for _, limit := range response.Limits {
		out := surface.UsageWindow{Kind: limit.Kind, Group: limit.Group, Percent: limit.Percent, Severity: limit.Severity}
		if limit.Scope != nil && limit.Scope.Model != nil {
			out.Scope = limit.Scope.Model.DisplayName
		}
		if at, err := time.Parse(time.RFC3339Nano, limit.ResetsAt); err == nil {
			out.ResetsAt = at.UTC()
		}
		usage.Limits = append(usage.Limits, out)
	}
	return usage, nil
}
