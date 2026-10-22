package surfaces

import (
	"context"
	"encoding/json"
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

// claudeWebAccounts holds each profile's account for as long as its cookie
// header is cached, so it sees a profile sign into another account as soon as
// the cookies themselves are read again.
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
	return root, ok && root.SignedInAs != nil && root.SignedInAs.AccountID != "", nil
}

func (c *Claude) accountCookies(ctx context.Context, root surface.ClaudeConfigRoot) (CookieSource, error) {
	candidates, err := signin.Candidates(c.signins, signin.Claude)
	if err != nil {
		return CookieSource{}, err
	}
	login := root.SignedInAs
	var seen []string
	for _, candidate := range candidates {
		source := c.cookies(candidate)
		web, err := c.webAccount(ctx, source)
		if err != nil {
			seen = append(seen, fmt.Sprintf("%s: %v", signinLabel(candidate), err))
			continue
		}
		if web.UUID == login.AccountID {
			return source, nil
		}
		seen = append(seen, fmt.Sprintf("%s is %s", signinLabel(candidate), web.Email))
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
	data, err := c.claudeGET(ctx, source, "/api/account")
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

func (c *Claude) claudeGET(ctx context.Context, source CookieSource, path string) (string, error) {
	request := c.request
	if request == nil {
		request = claudeSendRequest
	}
	status, data, err := request(ctx, "GET", "https://claude.ai"+path, c.headerMap("", ""), "", source, 15*time.Second)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("claude.ai %s returned HTTP %d", path, status)
	}
	return data, nil
}

// ClaudeConfigRoots describes each config root with the sign-in profile
// signed into its login and that login's usage as claude.ai reports it now.
func (c *Claude) ClaudeConfigRoots(ctx context.Context) ([]surface.ClaudeConfigRoot, error) {
	roots, err := surface.DescribeClaudeConfigRoots(c.home)
	if err != nil {
		return nil, err
	}
	for i := range roots {
		root := &roots[i]
		if root.SignedInAs == nil || root.SignedInAs.AccountID == "" || c.localOnly {
			continue
		}
		source, err := c.accountCookies(ctx, *root)
		if err != nil {
			root.SigninProblem = err.Error()
			continue
		}
		root.Signin = signinLabel(source.Signin)
		if root.Usage, err = c.claudeUsage(ctx, source, root.SignedInAs.OrganizationID); err != nil {
			root.UsageProblem = err.Error()
		}
	}
	return roots, nil
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

func (c *Claude) claudeUsage(ctx context.Context, source CookieSource, organization string) (*surface.ClaudeUsage, error) {
	if organization == "" {
		return nil, fmt.Errorf("the config root does not name its organization")
	}
	data, err := c.claudeGET(ctx, source, "/api/organizations/"+organization+"/usage")
	if err != nil {
		return nil, err
	}
	var response claudeUsageResponse
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return nil, fmt.Errorf("parse claude.ai usage: %w", err)
	}
	usage := &surface.ClaudeUsage{FetchedAt: time.Now().UTC()}
	for _, limit := range response.Limits {
		out := surface.ClaudeUsageLimit{Kind: limit.Kind, Group: limit.Group, Percent: limit.Percent, Severity: limit.Severity}
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
