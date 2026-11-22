package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/cfaccess"
	"github.com/zm2231/agenthail/internal/mcpoauth"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	remoteTokenLife     = time.Hour
	remoteSessionIdle   = time.Hour
	tokenInfoRemote     = "agenthail.remote"
	tokenInfoBinding    = "agenthail.binding"
	tokenInfoEpoch      = "agenthail.epoch"
	tokenInfoTokenHash  = "agenthail.token_hash"
	remoteInboxURIStart = "agenthail://inbox/"
)

type remoteMCP struct {
	d       *Daemon
	server  *mcp.Server
	handler http.Handler

	verifierMu   sync.Mutex
	verifier     *cfaccess.Verifier
	verifierKey  string
	keysRecorded string
	// certsURL and certsClient replace the team's key set, for tests.
	certsURL    string
	certsClient *http.Client

	mu       sync.Mutex
	sessions map[*mcp.ServerSession]*remotePushSession
	stopped  atomic.Bool
	// admitMu orders admitting a request against stopping, so every admitted one is drained.
	admitMu      sync.Mutex
	admitted     sync.WaitGroup
	stopCtx      context.Context
	stopRequests context.CancelFunc
	wake         chan struct{}
	// beforeNotice runs between a pass's last epoch check and its sends, and
	// afterEpoch between verify's epoch read and its token check, for tests.
	beforeNotice func()
	afterEpoch   func()

	oauth *oauthState

	eventWake chan struct{}
	// afterDueRead runs between the delivery loop's due read and its in-flight check, for tests.
	afterDueRead func()
	// webhookClient, eventRetries and eventTrialEvery are replaced by tests.
	webhookClient   *http.Client
	eventRetries    []time.Duration
	eventTrialEvery time.Duration
}

func (d *Daemon) remoteMCP() *remoteMCP {
	d.remoteOnce.Do(func() {
		m := &remoteMCP{d: d, sessions: map[*mcp.ServerSession]*remotePushSession{}, wake: make(chan struct{}, 1), eventWake: make(chan struct{}, 1), oauth: newOAuthState(), webhookClient: newWebhookClient(), eventRetries: eventRetrySchedule, eventTrialEvery: eventTrialEvery}
		m.stopCtx, m.stopRequests = context.WithCancel(context.Background())
		m.server = mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: d.build.Version}, &mcp.ServerOptions{
			SupportedProtocolVersions: mcpProtocolVersions,
			SubscribeHandler:          m.subscribe,
			UnsubscribeHandler:        func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
			Instructions:              "You are an Agenthail agent identity. Messages sent to you wait in your inbox. Subscribe to " + remoteInboxURIStart + "<your name> to be told when one arrives; the notice says only how many are unread. agenthail_wait with tasks [] returns when one arrives; agenthail_read with no sessionId returns your unread messages. Pass the ack it returns on your next read, or as cursor to agenthail_wait, to mark them read; until then they are returned again. Reply with agenthail_send to the sender each message names.",
		})
		m.server.AddReceivingMiddleware(m.track)
		d.registerMCPTools(m.server)
		m.registerEvents()
		m.server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: remoteInboxURIStart + "{name}", Name: "inbox", Title: "Inbox", Description: "How many messages wait unread in your inbox. Read them with agenthail_read.", MIMEType: "application/json"}, m.readInbox)
		stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m.server }, &mcp.StreamableHTTPOptions{EventStore: mcp.NewMemoryEventStore(nil), SessionTimeout: cmp.Or(d.remoteIdle, remoteSessionIdle)})
		m.handler = m.bearerChallenge(http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if refuseTunnelHost(w, r) {
				return
			}
			m.admitMu.Lock()
			if m.stopped.Load() {
				m.admitMu.Unlock()
				http.Error(w, "the daemon is stopping", http.StatusServiceUnavailable)
				return
			}
			m.admitted.Add(1)
			m.admitMu.Unlock()
			defer m.admitted.Done()
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			defer context.AfterFunc(m.stopCtx, cancel)()
			stream.ServeHTTP(w, r.WithContext(ctx))
		})))
		d.remote = m
	})
	return d.remote
}

func remoteRequest(r *http.Request) bool {
	token := bearerToken(r)
	return r.Header.Get(cfaccess.Header) != "" || strings.HasPrefix(token, registry.RemoteTokenPrefix) || strings.HasPrefix(token, registry.OAuthAccessPrefix) || (token == "" && (cloudflareProxied(r) || !localReach(r)))
}

func cloudflareProxied(r *http.Request) bool {
	return r.Header.Get("Cf-Ray") != "" || r.Header.Get("Cf-Connecting-Ip") != ""
}

// A URL-only client starts OAuth, or asks the person for a token, only when a probe without one gets a Bearer challenge.
func (m *remoteMCP) bearerChallenge(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var options auth.RequireBearerTokenOptions
		challenge := `Bearer realm="agenthail"`
		if issuer := m.d.oauthIssuer(); issuer != "" {
			options.ResourceMetadataURL = mcpoauth.ResourceMetadataURL(issuer)
			challenge += fmt.Sprintf(`, resource_metadata=%q`, options.ResourceMetadataURL)
		}
		if bearerToken(r) == "" {
			w.Header().Set("WWW-Authenticate", challenge)
			http.Error(w, "a bearer token is required", http.StatusUnauthorized)
			return
		}
		auth.RequireBearerToken(m.verify, &options)(next).ServeHTTP(w, r)
	})
}

// A tunnel that publishes more than /mcp still exposes nothing else.
func accessPathGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(cfaccess.Header) != "" && r.URL.Path != mcpoauth.ResourcePath && !slices.Contains(mcpoauth.Paths, r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *remoteMCP) accessVerifier(team, aud string) *cfaccess.Verifier {
	m.verifierMu.Lock()
	defer m.verifierMu.Unlock()
	if key := team + "\x00" + aud; m.verifier == nil || m.verifierKey != key {
		m.verifier, m.verifierKey = cfaccess.New(team, aud, m.certsURL, m.certsClient), key
	}
	return m.verifier
}

// Doctor runs in another process, so the key-set status goes to the registry.
func (m *remoteMCP) recordKeys(verifier *cfaccess.Verifier) {
	readAt, readErr := verifier.Status()
	m.verifierMu.Lock()
	key := readAt.String()
	if readErr != nil {
		key += readErr.Error()
	}
	changed := key != m.keysRecorded
	m.keysRecorded = key
	m.verifierMu.Unlock()
	if changed {
		if err := m.d.Registry.RecordAccessKeys(readAt, readErr); err != nil {
			m.d.logRuntimeError("record the Access key read", err)
		}
	}
}

type remoteRefusal struct{ reason string }

func (e remoteRefusal) Error() string { return e.reason }

func (m *remoteMCP) verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	d := m.d
	refuse := func(reason string) (*auth.TokenInfo, error) {
		d.logMCPAuthRefusal("remote", "-", "unauthenticated", remoteRefusal{reason})
		return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, reason)
	}
	epoch, err := d.Registry.RemoteEpoch()
	if err != nil {
		return nil, err
	}
	if m.afterEpoch != nil {
		m.afterEpoch()
	}
	if strings.HasPrefix(token, registry.OAuthAccessPrefix) {
		return m.verifyOAuth(token, r, epoch, refuse)
	}
	remote, err := d.Registry.RemoteIdentityByToken(token)
	if errors.Is(err, registry.ErrRemoteUnknown) {
		return refuse("the bearer is not a remote token of any identity here")
	}
	if err != nil {
		return nil, err
	}
	team, aud, configured, err := d.Registry.AccessSettings()
	if err != nil {
		return nil, err
	}
	assertion := r.Header.Get(cfaccess.Header)
	binding := "token only"
	switch {
	case configured && assertion == "":
		return refuse("Access is configured here, and this request did not come through it")
	case configured:
		verifier := m.accessVerifier(team, aud)
		claims, err := verifier.Verify(ctx, assertion)
		m.recordKeys(verifier)
		if err != nil {
			return refuse(err.Error())
		}
		switch {
		case claims.Subject == "" && remote.ClientID != "" && cfaccess.ClientID(claims.CommonName) == remote.ClientID:
			binding = "service token " + remote.ClientID
		case claims.Subject != "" && remote.Email != "" && strings.EqualFold(strings.TrimSpace(claims.Email), remote.Email):
			binding = "email " + remote.Email
		default:
			return refuse(fmt.Sprintf("the Access credential is not the one bound to @%s", remote.Name))
		}
	case assertion != "":
		return refuse("this request came through Access, and no Access settings are set here; run agenthail identity access set")
	}
	if err := d.Registry.TouchRemoteIdentity(remote.IdentityID, time.Now()); err != nil {
		d.logRuntimeError("remote mcp last seen", err)
	}
	return &auth.TokenInfo{UserID: remote.IdentityID, Expiration: time.Now().Add(remoteTokenLife), Extra: map[string]any{tokenInfoRemote: remote.Name, tokenInfoBinding: binding, tokenInfoEpoch: epoch, tokenInfoTokenHash: remote.TokenHash}}, nil
}

func (m *remoteMCP) verifyOAuth(token string, r *http.Request, epoch int64, refuse func(string) (*auth.TokenInfo, error)) (*auth.TokenInfo, error) {
	d := m.d
	if d.oauthIssuer() == "" {
		return refuse("OAuth is off here")
	}
	grant, expires, err := d.Registry.OAuthGrantByAccess(token)
	if errors.Is(err, registry.ErrOAuthUnknown) {
		return refuse("the bearer is not a live OAuth token of any identity here")
	}
	if err != nil {
		return nil, err
	}
	if _, _, configured, err := d.Registry.AccessSettings(); err != nil {
		return nil, err
	} else if !configured && r.Header.Get(cfaccess.Header) != "" {
		return refuse("this request came through Access, and no Access settings are set here; run agenthail identity access set")
	}
	if err := d.Registry.TouchOAuthGrant(grant.ID, time.Now()); err != nil {
		d.logRuntimeError("oauth grant last seen", err)
	}
	return &auth.TokenInfo{UserID: grant.IdentityID, Expiration: expires, Extra: map[string]any{tokenInfoRemote: grant.Identity, tokenInfoBinding: "oauth " + grant.ClientName, tokenInfoEpoch: epoch, tokenInfoTokenHash: grant.Credential()}}, nil
}

// An identity acts under the remote role, which by default has the person's
// reach, as the CLI it sends with does.
func (d *Daemon) remoteCaller(info *auth.TokenInfo) (*mcpCaller, error) {
	if info == nil {
		return nil, nil
	}
	name, ok := info.Extra[tokenInfoRemote].(string)
	if !ok {
		return nil, nil
	}
	record, err := d.Registry.Caller(info.UserID)
	if err != nil {
		return nil, fmt.Errorf("read @%s: %w", name, err)
	}
	caller, err := d.agentCaller(record)
	if err != nil {
		return nil, err
	}
	caller.actAs(d.callerRole(record, true))
	caller.remote, _ = info.Extra[tokenInfoBinding].(string)
	return caller, nil
}

func remoteName(extra *mcp.RequestExtra) (string, *auth.TokenInfo) {
	if extra == nil || extra.TokenInfo == nil {
		return "", nil
	}
	name, _ := extra.TokenInfo.Extra[tokenInfoRemote].(string)
	return name, extra.TokenInfo
}

func (d *Daemon) recordRemoteCall(caller *mcpCaller, tool string, refusal *mcpRefusal) {
	if caller == nil || caller.remote == "" {
		return
	}
	outcome, target := "ok", ""
	if refusal != nil {
		outcome, target = refusal.Code, refusal.target
	}
	if err := d.Registry.RecordHistory(registry.HistoryEntry{Kind: "mcp-remote", SessionID: target, SourceSessionID: caller.session.ID, Message: tool + " via " + caller.remote, Result: outcome}); err != nil {
		d.logRuntimeError("remote mcp audit", err)
	}
}

func clip(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
