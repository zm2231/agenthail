# Native apps and device pairing

Agenthail ships a native macOS app with the package and an iPhone companion that connects to the same daemon API. The browser dashboard remains available, but the native apps are the primary interactive surfaces.

## macOS

`/Applications/Agenthail.app` opens at login and owns both the full window and the menu bar item. It reads the local dashboard token from `~/.agenthail`, negotiates daemon protocol compatibility, and then uses the versioned `/api/v1` interface. Overview shows current work and connected surfaces. Conversations provides the transcript, context usage, model options, and safe controls. Operations manages queued work, relays, audit history, Tailscale access, and paired devices.

The package installer upgrades the app, CLI, daemon, sidecars, and skills together. A new app can talk to a compatible older daemon during the short replacement window. If the protocol ranges do not overlap, the app shows an update message instead of issuing commands against an unknown API.

## iPhone

The iPhone app requires Tailscale on both devices and does not expose the Mac to the public internet.

Production builds are archived, App Store signed, validated, and uploaded to TestFlight by the iOS job in `.github/workflows/release.yml`. The workflow uses automatic signing with an App Store Connect API key that can manage provisioning and upload builds. The `com.agenthail.ios` app record must exist in App Store Connect before the first upload.

1. Open Agenthail on the Mac.
2. Open Operations and enable Private phone access.
3. Choose Pair an iPhone.
4. In the iPhone app, scan the code.
5. Allow notifications if you want completion alerts.

The QR code contains a single-use secret that expires in five minutes. The resulting device credential is stored in the iPhone Keychain. Disconnecting the phone revokes its daemon credential and clears its local Keychain state; revoking it from the Mac immediately blocks daemon API access.

The iPhone app keeps the conversation picker and transcript as separate navigation screens. Current work is limited to open Claude Code sessions plus working, queued, or recently used Codex sessions. Disk-only Codex history remains readable and never presents a composer.

## Notifications

The Mac daemon triggers notifications when a newly observed turn completes or fails. It sends a generic completion status through the Agenthail push relay to APNs, never reply text. Push registration uses Apple App Attest with a fresh single-use challenge so arbitrary clients cannot create relay registrations. Tapping the notification opens the corresponding conversation. The app automatically renews its relay registration before its 90-day expiry or when its APNs token or environment changes. Turning notifications off removes the daemon and relay registrations. If a Mac is permanently unavailable, Settings can forget the local pairing after an explicit confirmation.

Production deployment requires an Apple Push Notification authentication key created in the Apple Developer portal for the Agenthail team. An App Store Connect API key cannot sign APNs requests. The Worker expects `APNS_KEY_P8`, `APNS_KEY_ID`, `APPLE_TEAM_ID`, and `APNS_TOPIC` secrets. `APNS_TOPIC` must match the iOS bundle ID.

The release workflow builds and validates the Mac package, iPhone archive, and relay before publication. Its final job creates a draft GitHub release, deploys and checks the matching relay, uploads the validated iPhone build, and only then makes the GitHub release public. `/health` reports the deployed relay version, protocol, and capabilities. If a relay deployment is unhealthy, redeploy the last known-good tag from `push-relay` and verify `/health` before retrying the native release.

## Protocol compatibility

Protocol 1 uses bearer authentication, JSON responses, typed error objects, and server-sent events. Native clients read `minimumProtocol` and `maximumProtocol` from `/api/v1/version` before loading state. Event cursors resume after network interruption; a cursor older than the daemon's bounded replay window receives `stream.reset` and refreshes from a snapshot.

Pairing credentials are independent from the older dashboard token and cookie. Rotating the dashboard token does not silently revoke native devices; use Operations to review and revoke them explicitly.
