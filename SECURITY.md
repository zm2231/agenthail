# Security

agenthail sits between your AI agent sessions and reads the browser cookies those sessions authenticate with. That is a lot to ask of you, so this document is specific about what it does, what it protects, and what it does not.

## Reporting a vulnerability

Email **zainmer@protonmail.com** with details and, if you have one, a reproduction. Please do not open a public issue for anything exploitable.

agenthail is pre-1.0 and moves quickly. Only the latest commit on `main` is supported; fixes land there rather than being backported.

## What agenthail is

A local daemon, CLI, and native Mac app. Your sessions, transcripts, prompts, replies, browser credentials, queue, and audit trail stay on your Mac except when Agenthail sends a message to the agent surface you chose. There is no Agenthail account or telemetry.

The optional iPhone notification path uses a small hosted relay because Apple Push Notification service does not accept delivery directly from a Mac daemon. When a paired phone grants notification permission, the relay stores its opaque APNs device token, APNs environment, and a hash of a random per-installation capability. For a notification, the daemon sends that capability with a bounded title, completion status, session ID, and event type. The relay forwards the payload to Apple and does not persist it. It never receives a transcript, prompt, reply, browser cookie, dashboard token, device bearer token, or Tailscale credential. Disabling notifications or revoking the paired device stops the daemon from sending to that installation.

State lives in `~/.agenthail`: a SQLite registry, plus the daemon lock, PID, and log. Delivery history is bounded and records what the daemon decided (queued, sent, retried, failed) without copying whole transcripts.

## Trust boundaries

**Browser cookies.** Claude Code and Notion surfaces authenticate as you, using the Chrome profile you are already signed into. A Node bridge (`sidecar/cookie.mjs`) reads fresh cookies for a specific URL and passes them over a pipe to the Python sidecar, which attaches them to that one request. They are not written to disk and not written to the log. If the bridge fails, the request stops there; it never falls back to an unauthenticated or weaker path. `AGENTHAIL_DEBUG=1` prints diagnostics with cookie values withheld.

Anyone who can already read your home directory can already read these cookies from Chrome directly. agenthail does not widen that boundary, but it does not narrow it either.

**The dashboard.** The signed package and source installer enable the dashboard on loopback so the native app can connect immediately. A manual binary setup remains off until `agenthail dashboard enable`. The listener is local-only unless you explicitly enable Tailscale access in Operations.

When enabled, it is constrained in four ways:

- It only accepts a loopback listener. A non-loopback address is rejected at config validation, so you cannot point it at `0.0.0.0` even on purpose (`internal/daemon/dashboard_config.go`). The default is `127.0.0.1:7412`.
- Every request needs a per-install access token, compared in constant time (`crypto/subtle`). Without it you get a `401`.
- The session cookie is `HttpOnly` and `SameSite=Strict`. Its value is the token itself, so treat it as one; rotating the token revokes every saved device.
- Any request that is not `GET` or `HEAD` must also pass a same-origin check, so a page in another tab cannot drive your agents.
- Responses carry a strict `Content-Security-Policy` (`default-src 'self'`, `script-src 'self'`, `frame-ancestors 'none'`, `base-uri 'none'`), plus `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, and `X-Frame-Options: DENY`.

**Remote access.** `agenthail dashboard remote` configures a Tailscale Serve route reachable only from your own tailnet. agenthail stays bound to loopback, while Tailscale terminates HTTPS with an automatically provisioned certificate and proxies to the local HTTP listener. The local listener is never exposed directly.

When you open the phone link, the query-string token is accepted only for the bootstrap request. agenthail sets an `HttpOnly`, `SameSite=Strict` cookie and immediately redirects to `/`, removing the token from the visible URL and the saved dashboard location. The original link and QR code still contain the full dashboard credential, so treat either one like a password. The authenticated cookie lasts one year so an Add to Home Screen installation remains signed in; rotating the dashboard token revokes it.

Tailscale Funnel exposes a node to the public internet. agenthail refuses to enable remote access while Funnel is on its port, and reports an error rather than proceeding (`internal/daemon/remote_access.go`). It never turns Funnel on.

The browser phone URL and its QR code **contain the dashboard token**. Treat them as credentials. The native iPhone pairing QR uses a separate single-use secret instead. Rotating the local dashboard token revokes browser access and the one-year trusted-device cookie, but does not revoke native paired devices.

**Native device pairing.** The native iPhone app does not use the dashboard cookie. The Mac creates a random, single-use pairing secret that expires after five minutes. Completing the pairing exchanges it for a random bearer token stored as a hash on the Mac and in the iPhone Keychain. Device tokens are scoped; the iPhone receives read and control access, not settings access. Revoking a device disables that token immediately. The app talks directly to the daemon through the existing tailnet-only HTTPS route.

**Push notifications.** Push is off until a paired iPhone grants Apple notification permission. Registration requires a fresh, single-use challenge and a valid Apple App Attest statement from the signed iPhone app. The relay capability is separate from the daemon bearer token and only authorizes notification delivery to one APNs installation. Agenthail stores the capability on the Mac and in the iPhone Keychain. The hosted relay stores only its hash. Cloudflare handles the relay request and Apple handles final delivery, so both are outside the local trust boundary. Notification previews can appear on a locked device according to the user's iOS settings, so Agenthail sends only generic completion or failure status and never includes reply text.

**The Codex bridge.** `agenthail launch codex` exposes Chromium's renderer debugger on loopback only, and asks the renderer to use Codex Desktop's existing app-server connection. It does not write to the app-server child process and does not launch Codex with Node's `--inspect` flag. Any process running as your user can reach a loopback debugging port, which is the same exposure as any Chromium instance launched with remote debugging.

## Accountability: nothing moves without a record

agenthail can put instructions into agents that write files and run commands, sometimes while you are away from the machine. The check on that is that it cannot do any of it silently.

Every message it moves is written to a local audit trail before and after delivery, with the event kind, the target session, the session it came from, the route and queue IDs that caused it, the message body, the result, and any error. The kinds are specific rather than a generic "activity" log:

| Event | Meaning |
|---|---|
| `queued` | accepted for a busy target, not yet delivered |
| `sent` / `delivered` | reached the agent |
| `busy` | target was mid-turn and the delivery did not happen |
| `failed` | delivery failed |
| `unknown` | may have reached the agent; deliberately not retried |
| `ack-error` | delivered, but the local acknowledgement failed |
| `retry` | a failed delivery was rescheduled |
| `canceled` | you withdrew a pending instruction |
| `relay` | one agent's completed output was handed to another |
| `relay-dropped` | a relay was filtered out or hit the hop limit |

Read it from the dashboard under Operations → Audit, or from the CLI with `agenthail history` and `agenthail history --json`.

Pending work is inspectable **before** it reaches an agent, and can be withdrawn:

```bash
agenthail queue list --all
agenthail queue rm 12
agenthail queue clear @writer
```

This is the practical mitigation for the relay risks below. A regex filter cannot decide whether relayed text is hostile, but the trail always shows exactly what crossed, which session produced it, and which route carried it. If an agent starts acting on something you did not write, `agenthail history` is where you find out what it was told and where that came from.

The trail never leaves your machine. It is bounded on purpose (the newest 2000 events, each field capped at 16 KiB) so it records the daemon's decisions without becoming a second copy of every transcript. Recording is treated as observability and never as delivery, so a failure to write history cannot fail a message that actually went out.

## What agenthail does not protect against

Stating these plainly is more useful than implying they are covered.

- **A hostile local process running as your user.** It can read `~/.agenthail`, the dashboard token, your Chrome cookies, and the loopback debugging port. agenthail assumes your user account is not already compromised.
- **Prompt injection.** A relay delivers one agent's completed output into another agent's input. If an agent can be induced to emit attacker-chosen text (from a web page it fetched, a file it read, a repo it cloned), that text becomes the next agent's instructions. Relay filters are a regular expression on content, and a regex is not a security boundary. Route agents you trust, use filters to narrow what crosses, and rely on the audit trail above to see what actually did.
- **The agent's own permissions.** agenthail sends messages; it does not add a sandbox. An instruction relayed into Codex runs with exactly the permissions that Codex session already had.
- **Surface changes upstream.** Claude Code, Codex, and Notion can change their integration surfaces at any time. `agenthail doctor` reports what still works.

## Delivery safety

These are correctness properties, and they exist because a coordination bug in this position is a security-adjacent problem.

Relay routes are validated as a graph at creation, so a self-route or a cycle (`@a → @b → @a`) is rejected before it can exist. This graph check is currently the only thing preventing a relay loop, so treat it as load-bearing when you add routes. Relay payloads are truncated before entering the next agent's context.

If the daemon dies in the window where a message may have reached an agent but was not acknowledged locally, the outcome is recorded as **unknown** and the message is not resent. You decide whether to retry. Silently issuing an instruction twice to an agent that can write files is worse than stopping and asking.

Upstream responses are read in chunks with a 16 MiB default ceiling (`AGENTHAIL_MAX_RESPONSE_BYTES`). Long Claude Code transcript records raise an explicit error at 32 MiB instead of truncating silently.

## Releases

Release archives are built from a clean worktree, embed the exact revision and build time, sign the macOS binary with a Developer ID identity, and submit it for notarization. A production build fails closed if signing or notarization is unavailable. Each archive ships with a SHA-256 checksum.
