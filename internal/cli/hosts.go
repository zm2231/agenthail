package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

const hostsUsage = `usage: agenthail hosts [list] [--json]
       agenthail hosts invite
       agenthail hosts add <pairing-link> [--address <tailnet-ip>] [--name <name>]
       agenthail hosts remove <host>
       agenthail hosts name [new-name]

Paired hosts are other Agenthail daemons on your tailnet. Target their sessions as host::target.`

type hostRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
}

type remoteTarget struct {
	ID      string              `json:"id"`
	Surface surface.SurfaceKind `json:"surface"`
	Name    string              `json:"name"`
	Alias   string              `json:"alias,omitempty"`
	Host    hostRef             `json:"host"`
}

func (t remoteTarget) label() string {
	_, raw, _ := hosts.Split(t.ID)
	if t.Alias != "" {
		raw = "@" + t.Alias
	}
	return t.Host.Name + hosts.Separator + raw
}

func (t remoteTarget) session() *surface.Session {
	return &surface.Session{ID: t.ID, Surface: t.Surface, Name: t.Name}
}

type targetNotFoundError struct{ target string }

func (e targetNotFoundError) Error() string { return fmt.Sprintf("no session matched '%s'", e.target) }

func (a *App) resolveTarget(ctx context.Context, target string) (*surface.Session, surface.Surface, error) {
	session, adapter, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return nil, nil, err
	}
	if remote != nil {
		return nil, nil, fmt.Errorf("%s is a session on host %s; this command is not available for sessions on another host yet", remote.label(), remote.Host.Name)
	}
	return session, adapter, nil
}

// resolveAnyTarget resolves host::target on the named host and an
// unqualified target across this host and every paired host. A target that
// matches on more than one host is an error, never a pick.
func (a *App) resolveAnyTarget(ctx context.Context, target string) (*surface.Session, surface.Surface, *remoteTarget, error) {
	if a.Registry == nil {
		session, adapter, err := a.resolveLocalTarget(ctx, target)
		return session, adapter, nil, err
	}
	identity, err := a.Registry.HostIdentity()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read host identity: %w", err)
	}
	if hostText, rest, ok := hosts.Split(target); ok {
		if strings.EqualFold(hostText, identity.ID) || strings.EqualFold(hostText, identity.Name) {
			session, adapter, err := a.resolveLocalTarget(ctx, rest)
			return session, adapter, nil, err
		}
		host, err := a.Registry.Host(hostText)
		if errors.Is(err, registry.ErrHostNotFound) {
			return nil, nil, nil, fmt.Errorf("no paired host is named %q; see 'agenthail hosts'", hostText)
		}
		if err != nil {
			return nil, nil, nil, err
		}
		matches, err := a.remoteMatches(ctx, host.ID, rest)
		if err != nil {
			return nil, nil, nil, err
		}
		switch len(matches) {
		case 0:
			return nil, nil, nil, fmt.Errorf("no session matched '%s' on host %s", rest, host.Name)
		case 1:
			return a.remoteResolved(matches[0])
		}
		return nil, nil, nil, ambiguousTargetError(target, nil, matches)
	}
	paired, err := a.Registry.ListHosts()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list paired hosts: %w", err)
	}
	session, adapter, localErr := a.resolveLocalTarget(ctx, target)
	if len(paired) == 0 {
		return session, adapter, nil, localErr
	}
	var notFound targetNotFoundError
	if localErr != nil && !errors.As(localErr, &notFound) && !errors.Is(localErr, sql.ErrNoRows) {
		return nil, nil, nil, localErr
	}
	matches, err := a.remoteMatches(ctx, "", target)
	if err != nil {
		return nil, nil, nil, err
	}
	switch {
	case len(matches) == 0:
		return session, adapter, nil, localErr
	case localErr != nil && len(matches) == 1:
		return a.remoteResolved(matches[0])
	}
	var local []string
	if localErr == nil {
		local = append(local, identity.Name+hosts.Separator+a.resolveDisplay(session.ID))
	}
	return nil, nil, nil, ambiguousTargetError(target, local, matches)
}

func (a *App) remoteResolved(match remoteTarget) (*surface.Session, surface.Surface, *remoteTarget, error) {
	if !a.daemonIsRunning() {
		return nil, nil, nil, fmt.Errorf("%s is a session on host %s, reached through the local daemon, which is not running; start it with: agenthail daemon start", match.label(), match.Host.Name)
	}
	return nil, nil, &match, nil
}

func ambiguousTargetError(target string, local []string, remote []remoteTarget) error {
	labels := append([]string{}, local...)
	for _, match := range remote {
		labels = append(labels, match.label())
	}
	return fmt.Errorf("ambiguous target %q matched %s; qualify it as host::target", target, strings.Join(labels, ", "))
}

func (a *App) remoteMatches(ctx context.Context, hostID, target string) ([]remoteTarget, error) {
	if a.daemonIsRunning() {
		client, err := a.daemonAPI()
		if err != nil {
			return nil, err
		}
		query := url.Values{"scope": []string{"remote"}, "target": []string{target}}
		if hostID != "" {
			query.Set("host", hostID)
		}
		var answer struct {
			Matches []remoteTarget `json:"matches"`
			Errors  []string       `json:"errors"`
		}
		if err := client.getJSON(ctx, "/api/v1/resolve", query, &answer); err != nil {
			return nil, fmt.Errorf("resolve %q on paired hosts: %w", target, err)
		}
		if len(answer.Errors) > 0 {
			return nil, fmt.Errorf("ambiguous target %q: %s", target, strings.Join(answer.Errors, "; "))
		}
		return answer.Matches, nil
	}
	found, err := a.Registry.MatchHostSessions(hostID, target)
	if err != nil {
		return nil, fmt.Errorf("match mirrored sessions: %w", err)
	}
	paired, err := a.Registry.ListHosts()
	if err != nil {
		return nil, err
	}
	names := map[string]registry.PairedHost{}
	for _, host := range paired {
		names[host.ID] = host
	}
	matches := make([]remoteTarget, 0, len(found))
	for _, match := range found {
		host := names[match.HostID]
		matches = append(matches, remoteTarget{ID: hosts.Qualify(match.HostID, match.ID), Surface: match.Surface, Name: match.Name, Alias: match.Alias, Host: hostRef{ID: host.ID, Name: host.Name, State: host.State}})
	}
	return matches, nil
}

func remoteReceipt(data []byte) (delivery.Receipt, error) {
	var response struct {
		Status string           `json:"status"`
		Result delivery.Receipt `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return delivery.Receipt{}, fmt.Errorf("decode daemon receipt: %w", err)
	}
	if response.Result.Status == "" && response.Status == string(registry.DeliveryIntentSubmitted) {
		return delivery.Receipt{Evidence: surface.EvidenceUnknown, Status: response.Status, Detail: "the request is still being applied; its outcome is unknown until the host confirms it"}, nil
	}
	return response.Result, nil
}

func (a *App) sendRemote(ctx context.Context, remote *remoteTarget, message string, args []string, timeout time.Duration) error {
	wantStream, wantReply, jsonOut := hasFlag(args, "--stream"), hasFlag(args, "--reply"), hasFlag(args, "--json")
	if hasFlag(args, "--no-queue") {
		return fmt.Errorf("--no-queue is not available for %s: the daemon holds a send to another host until that host answers", remote.label())
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	turnOptions, err := parseTurnOptions(args)
	if err != nil {
		return err
	}
	payload, err := actionPayload(turnOptions)
	if err != nil {
		return err
	}
	payload["action"], payload["sessionId"], payload["message"] = "send", remote.ID, message
	source, err := a.sourceSessionID(ctx, flagVal(args, "--from"))
	if err != nil {
		return err
	}
	if source != "" {
		payload["sourceSessionId"] = source
	}
	if model := flagVal(args, "--model"); model != "" {
		payload["model"] = model
	}
	var stream sessionstream.Subscription
	if wantStream || wantReply {
		stream, err = a.prepareDaemonSessionStream(ctx, nil, remote.session())
		if err != nil {
			return err
		}
		if stream.Cancel != nil {
			defer stream.Cancel()
		}
	}
	data, _, err := client.action(ctx, payload)
	if err != nil {
		return fmt.Errorf("send to %s: %w", remote.label(), err)
	}
	receipt, err := remoteReceipt(data)
	if err != nil {
		return err
	}
	if receipt.Evidence == surface.EvidenceQueued || receipt.Status == string(registry.DeliveryIntentSubmitted) {
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(receipt)
		}
		if receipt.Evidence == surface.EvidenceQueued {
			fmt.Printf("queued for %s: %s\n", remote.label(), receipt.Detail)
		} else {
			fmt.Printf("delivery submitted to %s; %s\n", remote.label(), receipt.Detail)
		}
		return nil
	}
	if wantStream {
		return consumeDaemonStreamOutput(ctx, stream, receipt.TurnID, timeout)
	}
	if wantReply {
		reply, err := consumeSessionReply(ctx, stream, receipt.TurnID)
		if err != nil {
			return err
		}
		receipt.Evidence = surface.EvidenceReplyObserved
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"delivery": receipt, "reply": reply})
		}
		if reply != nil && reply.Text != "" {
			fmt.Println(reply.Text)
		}
		return nil
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(receipt)
	}
	fmt.Printf("sent to %s (turn %s)\n", remote.label(), receipt.TurnID)
	return nil
}

func actionPayload(options surface.TurnOptions) (map[string]any, error) {
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (a *App) steerRemote(ctx context.Context, remote *remoteTarget, message string) error {
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	data, _, err := client.action(ctx, map[string]any{"action": "steer", "sessionId": remote.ID, "message": message})
	if err != nil {
		return fmt.Errorf("steer %s: %w", remote.label(), err)
	}
	receipt, err := remoteReceipt(data)
	if err != nil {
		return err
	}
	if receipt.Status == string(registry.DeliveryIntentSubmitted) {
		fmt.Printf("Submitted to %s.\n", remote.label())
	} else {
		fmt.Printf("Sent to %s.\n", remote.label())
	}
	return nil
}

func (a *App) interruptRemote(ctx context.Context, remote *remoteTarget) error {
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	if _, _, err := client.action(ctx, map[string]any{"action": "interrupt", "sessionId": remote.ID}); err != nil {
		return fmt.Errorf("interrupt %s: %w", remote.label(), err)
	}
	return nil
}

func (a *App) isLocalHost(ref string) (bool, error) {
	identity, err := a.Registry.HostIdentity()
	if err != nil {
		return false, fmt.Errorf("read host identity: %w", err)
	}
	return strings.EqualFold(ref, identity.ID) || strings.EqualFold(ref, identity.Name), nil
}

func (a *App) createRemoteThread(hostText string, request threadCreateRequest, args []string) error {
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	payload, err := actionPayload(request.options.TurnOptions)
	if err != nil {
		return err
	}
	fields := map[string]string{"action": "session-create", "host": hostText, "surface": string(request.surface), "message": request.message, "cwd": request.cwd, "alias": request.alias, "model": request.model, "approvalPolicy": request.approval, "name": request.options.Name, "worktree": request.options.Worktree, "agent": request.options.Agent, "permissionMode": request.options.PermissionMode}
	for key, value := range fields {
		if value != "" {
			payload[key] = value
		}
	}
	data, status, err := client.action(ctx, payload)
	var response struct {
		OK         bool                `json:"ok"`
		Status     string              `json:"status"`
		Retryable  bool                `json:"retryable"`
		Detail     string              `json:"detail"`
		Warning    string              `json:"warning"`
		DeliveryID int64               `json:"deliveryId"`
		Session    *surface.Session    `json:"session"`
		Result     *surface.SendResult `json:"result"`
		Error      string              `json:"error"`
	}
	if err != nil {
		var statusErr hosts.StatusError
		if !errors.As(err, &statusErr) || statusErr.Status != http.StatusBadGateway {
			return fmt.Errorf("create on host %s: %w", hostText, err)
		}
		response.Error = statusErr.Message
	} else if decodeErr := json.Unmarshal(data, &response); decodeErr != nil {
		return fmt.Errorf("decode create response (HTTP %d): %w", status, decodeErr)
	}
	output := threadCreateOutput{OK: err == nil && response.OK, Status: response.Status, Retryable: response.Retryable, Detail: response.Detail, Warning: response.Warning, DeliveryID: response.DeliveryID, Session: response.Session, Delivery: response.Result, Alias: request.alias, Error: response.Error}
	output.Unknown = output.Status == string(registry.DeliveryIntentSubmitted)
	if !output.OK {
		if output.Error == "" {
			output.Error = fmt.Sprintf("create on host %s failed", hostText)
		}
		return emitThreadFailure(request, output, errors.New(output.Error))
	}
	if request.jsonOut {
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	if output.Session == nil {
		return fmt.Errorf("host %s created no session", hostText)
	}
	_, raw, _ := hosts.Split(output.Session.ID)
	label := hostText + hosts.Separator + string(output.Session.Surface) + "/" + raw
	if request.alias != "" {
		label = hostText + hosts.Separator + "@" + request.alias
	}
	switch {
	case output.Unknown:
		fmt.Printf("created %s in %s; first turn submitted: %s\n", label, output.Session.Cwd, output.Detail)
	case output.Delivery != nil && output.Delivery.UUID != "":
		fmt.Printf("created %s in %s and started turn %s\n", label, output.Session.Cwd, output.Delivery.UUID)
	default:
		fmt.Printf("created %s in %s\n", label, output.Session.Cwd)
	}
	return nil
}

func (a *App) cmdHosts(args []string) error {
	if hasFlag(args, "--help") {
		fmt.Println(hostsUsage)
		return nil
	}
	positional := stripFlags(args)
	sub := "list"
	if len(positional) > 0 {
		sub, positional = positional[0], positional[1:]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	switch sub {
	case "list", "ls":
		if len(positional) != 0 {
			return errors.New(hostsUsage)
		}
		return hostsList(ctx, client, hasFlag(args, "--json"))
	case "invite":
		if len(positional) != 0 {
			return errors.New(hostsUsage)
		}
		var invite struct {
			PairingURL string    `json:"pairingURL"`
			ExpiresAt  time.Time `json:"expiresAt"`
		}
		data, _, err := client.call(ctx, http.MethodPost, "/api/v1/pairings", nil, map[string]any{"name": "Agenthail host", "scopes": []string{"read", "control", registry.HostScope}}, nil)
		if err != nil {
			return fmt.Errorf("create host invite: %w", err)
		}
		if err := json.Unmarshal(data, &invite); err != nil {
			return err
		}
		fmt.Printf("On the other host run, before %s:\n  agenthail hosts add '%s'\n", invite.ExpiresAt.Local().Format(time.Kitchen), invite.PairingURL)
		return nil
	case "add":
		if len(positional) != 1 {
			return errors.New(hostsUsage)
		}
		var added struct {
			Host registry.PairedHost `json:"host"`
		}
		data, _, err := client.call(ctx, http.MethodPost, "/api/v1/hosts", nil, map[string]string{"pairingUrl": positional[0], "address": flagVal(args, "--address"), "name": flagVal(args, "--name")}, nil)
		if err != nil {
			return fmt.Errorf("pair host: %w", err)
		}
		if err := json.Unmarshal(data, &added); err != nil {
			return err
		}
		fmt.Printf("paired %s (%s) at %s; target its sessions as %s::<target>\n", added.Host.Name, added.Host.ID, added.Host.Address, added.Host.Name)
		return nil
	case "remove", "rm":
		if len(positional) != 1 {
			return errors.New(hostsUsage)
		}
		if _, _, err := client.call(ctx, http.MethodDelete, "/api/v1/hosts", nil, map[string]string{"host": positional[0]}, nil); err != nil {
			return fmt.Errorf("remove host: %w", err)
		}
		fmt.Printf("removed %s and its sessions from this host\n", positional[0])
		return nil
	case "name":
		var self struct {
			Host hostRef `json:"host"`
		}
		switch len(positional) {
		case 0:
			if err := client.getJSON(ctx, "/api/v1/host", nil, &self); err != nil {
				return err
			}
		case 1:
			data, _, err := client.call(ctx, http.MethodPut, "/api/v1/host", nil, map[string]string{"name": positional[0]}, nil)
			if err != nil {
				return fmt.Errorf("rename host: %w", err)
			}
			if err := json.Unmarshal(data, &self); err != nil {
				return err
			}
		default:
			return errors.New(hostsUsage)
		}
		fmt.Printf("%s (%s)\n", self.Host.Name, self.Host.ID)
		return nil
	}
	return fmt.Errorf("unknown hosts command %q\n%s", sub, hostsUsage)
}

func hostsList(ctx context.Context, client *daemonAPIClient, jsonOut bool) error {
	var listing struct {
		Host  hostRef `json:"host"`
		Hosts []struct {
			registry.PairedHost
			Detail       string `json:"detail"`
			Notice       string `json:"notice"`
			SessionCount int    `json:"sessionCount"`
		} `json:"hosts"`
	}
	data, _, err := client.call(ctx, http.MethodGet, "/api/v1/hosts", nil, nil, nil)
	if err != nil {
		return err
	}
	if jsonOut {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := json.Unmarshal(data, &listing); err != nil {
		return err
	}
	fmt.Printf("this host: %s (%s)\n", listing.Host.Name, listing.Host.ID)
	if len(listing.Hosts) == 0 {
		fmt.Println("no paired hosts; pair one with 'agenthail hosts invite' on the other host")
		return nil
	}
	fmt.Printf("%-16s %-14s %-13s %-8s %-16s %s\n", "HOST", "ID", "STATE", "SESSIONS", "ADDRESS", "LAST CONTACT")
	for _, host := range listing.Hosts {
		contact := "never"
		if at, err := time.Parse(time.RFC3339Nano, host.LastContactAt); err == nil {
			contact = relTime(at)
		}
		fmt.Printf("%-16s %-14s %-13s %-8d %-16s %s\n", truncate(host.Name, 16), host.ID, host.State, host.SessionCount, host.Address, contact)
		if detail := host.Detail; detail != "" {
			fmt.Printf("  %s\n", detail)
		} else if host.LastError != "" {
			fmt.Printf("  %s\n", host.LastError)
		}
		if host.Notice != "" {
			fmt.Printf("  %s\n", host.Notice)
		}
	}
	return nil
}

type listHostRow struct {
	Host        hostRef
	Alias       string
	Unavailable string
	QueueCount  int
}

func listRemoteSession(record registry.CatalogSessionState) (surface.Session, listHostRow) {
	var projection struct {
		Alias      string            `json:"alias"`
		Subagent   *surface.Subagent `json:"subagent"`
		Host       *hostRef          `json:"host"`
		QueueCount int               `json:"queueCount"`
	}
	_ = json.Unmarshal([]byte(record.ProjectionFingerprint), &projection)
	session := record.Session
	session.ID = hosts.Qualify(record.HostID, record.Session.ID)
	session.Subagent = projection.Subagent
	row := listHostRow{Host: hostRef{ID: record.HostID, Name: record.HostID}, Alias: projection.Alias, Unavailable: record.UnavailableReason, QueueCount: projection.QueueCount}
	if projection.Host != nil {
		row.Host = *projection.Host
	}
	return session, row
}

type listSessionView struct {
	surface.Session
	Host              *hostRef `json:"host,omitempty"`
	UnavailableReason string   `json:"unavailableReason,omitempty"`
}

// channelMember resolves a channel member on this host or a paired host. A
// member on a paired host is stored as its mirrored row; whether it can take
// a message is that host's call when the channel sends.
func (a *App) channelMember(ctx context.Context, target string, joining bool) (string, string, error) {
	session, adapter, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return "", "", err
	}
	if remote != nil {
		_, raw, _ := hosts.Split(remote.ID)
		return raw, remote.label(), nil
	}
	if joining {
		if err := a.ensureWritableTarget(ctx, session, adapter); err != nil {
			return "", "", err
		}
	}
	return session.ID, a.resolveDisplay(session.ID), nil
}

func relayRef(session *surface.Session, remote *remoteTarget) string {
	if remote != nil {
		return remote.ID
	}
	return session.ID
}

// addHostRelay creates a relay with an end on a paired host through the
// daemon, which stores it with its source: on that host when the source is
// there, otherwise here.
func (a *App) addHostRelay(ctx context.Context, from *surface.Session, fromRemote *remoteTarget, to *surface.Session, toRemote *remoteTarget, pattern string, once bool, handoff string) error {
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	payload := map[string]any{"action": "relay-add", "fromId": relayRef(from, fromRemote), "toId": relayRef(to, toRemote), "pattern": pattern, "once": once, "handoff": handoff}
	if _, _, err := client.action(ctx, payload); err != nil {
		return err
	}
	where := "this host"
	if fromRemote != nil {
		where = "host " + fromRemote.Host.Name
	}
	fromLabel, toLabel := a.resolveDisplay(relayRef(from, fromRemote)), a.resolveDisplay(relayRef(to, toRemote))
	if fromRemote != nil {
		fromLabel = fromRemote.label()
	}
	if toRemote != nil {
		toLabel = toRemote.label()
	}
	fmt.Printf("relay on %s: %s -> %s (pattern /%s/, %s)\n", where, fromLabel, toLabel, pattern, handoff)
	return nil
}

func (a *App) removeHostRelay(hostRef, relay string) error {
	id, err := strconv.ParseInt(strings.TrimPrefix(relay, "#"), 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid relay id %q", relay)
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	if _, _, err := client.action(context.Background(), map[string]any{"action": "relay-remove", "relayId": id, "host": hostRef}); err != nil {
		return err
	}
	fmt.Printf("removed relay %s%s%d\n", hostRef, hosts.Separator, id)
	return nil
}

type hostRelays struct {
	Host   string              `json:"host"`
	Relays []registry.RouteRow `json:"relays,omitempty"`
	Error  string              `json:"error,omitempty"`
}

// pairedHostRelays reads each paired host's relays through the daemon. A
// host that cannot answer is listed with why.
func (a *App) pairedHostRelays(ctx context.Context) ([]hostRelays, error) {
	paired, err := a.Registry.ListHosts()
	if err != nil || len(paired) == 0 {
		return nil, err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return nil, err
	}
	result := make([]hostRelays, 0, len(paired))
	for _, host := range paired {
		var page struct {
			Relays []registry.RouteRow `json:"relays"`
		}
		entry := hostRelays{Host: host.Name}
		if err := client.getJSON(ctx, "/api/v1/relays", url.Values{"host": []string{host.ID}}, &page); err != nil {
			entry.Error = err.Error()
		} else {
			entry.Relays = page.Relays
		}
		result = append(result, entry)
	}
	return result, nil
}

// sessionDisplay names a session on this host or a paired host: a raw
// mirrored id or a host-qualified id reads as host::@alias or
// host::surface/name, the way its host lists it.
func (a *App) sessionDisplay(id string) string {
	if a.Registry == nil {
		return truncate(id, 24)
	}
	hostID, raw, qualified := hosts.Split(id)
	if !qualified {
		owner, err := a.Registry.SessionHost(id)
		if err != nil || owner == "" {
			return a.resolveDisplay(id)
		}
		hostID = owner
	} else if identity, err := a.Registry.HostIdentity(); err == nil && identity.ID == hostID {
		return a.resolveDisplay(raw)
	}
	name := hostID
	if paired, err := a.Registry.ListHosts(); err == nil {
		for _, host := range paired {
			if host.ID == hostID {
				name = host.Name
			}
		}
	}
	rows, err := a.Registry.HostCatalogRows(hostID)
	if err == nil {
		for _, record := range rows {
			if record.Session.ID != raw {
				continue
			}
			session, row := listRemoteSession(record)
			if row.Alias != "" {
				return name + hosts.Separator + "@" + row.Alias
			}
			if session.Name != "" {
				return name + hosts.Separator + fmt.Sprintf("%s/%s", session.Surface, truncate(session.Name, 20))
			}
		}
	}
	return name + hosts.Separator + truncate(raw, 24)
}
