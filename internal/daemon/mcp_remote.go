package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/cfaccess"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	remoteTokenLife  = time.Hour
	tokenInfoRemote  = "agenthail.remote"
	tokenInfoBinding = "agenthail.binding"
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
}

// Stateless: every request is verified on its own, so a revoked token stops at its next request.
func (d *Daemon) remoteMCP() *remoteMCP {
	d.remoteOnce.Do(func() {
		m := &remoteMCP{d: d}
		m.server = mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: d.build.Version}, &mcp.ServerOptions{
			SupportedProtocolVersions: mcpProtocolVersions,
			Instructions:              "You are an Agenthail agent identity. Messages sent to you wait in your inbox. agenthail_wait with tasks [] returns when one arrives; agenthail_read with no sessionId returns your unread messages. Pass the ack it returns on your next read, or as cursor to agenthail_wait, to mark them read; until then they are returned again. Reply with agenthail_send to the sender each message names.",
		})
		d.registerMCPTools(m.server)
		stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m.server }, &mcp.StreamableHTTPOptions{Stateless: true})
		bearer := auth.RequireBearerToken(m.verify, &auth.RequireBearerTokenOptions{})
		m.handler = bearer(http.NewCrossOriginProtection().Handler(stream))
		d.remote = m
	})
	return d.remote
}

func remoteRequest(r *http.Request) bool {
	return r.Header.Get(cfaccess.Header) != "" || strings.HasPrefix(bearerToken(r), registry.RemoteTokenPrefix)
}

// A tunnel that publishes more than /mcp still exposes nothing else.
func accessPathGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(cfaccess.Header) != "" && r.URL.Path != "/mcp" {
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
	return &auth.TokenInfo{UserID: remote.IdentityID, Expiration: time.Now().Add(remoteTokenLife), Extra: map[string]any{tokenInfoRemote: remote.Name, tokenInfoBinding: binding}}, nil
}

// An identity has the person's reach, as the CLI it sends with does.
func (d *Daemon) remoteCaller(info *auth.TokenInfo) (*mcpCaller, error) {
	if info == nil {
		return nil, nil
	}
	name, ok := info.Extra[tokenInfoRemote].(string)
	if !ok {
		return nil, nil
	}
	session, err := d.Registry.Session(info.UserID)
	if err != nil {
		return nil, fmt.Errorf("read @%s: %w", name, err)
	}
	caller, err := d.agentCaller(*session)
	if err != nil {
		return nil, err
	}
	for _, capability := range operatorCapabilities {
		if !caller.has(capability) {
			caller.capabilities = append(caller.capabilities, capability)
		}
	}
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
