# Push notification service

This is a maintainer reference for the Cloudflare Worker that delivers generic iPhone notifications through Apple Push Notification service.

## Credentials

The deployment requires:

- `APNS_KEY_P8`: an APNs authentication key created in the Apple Developer portal
- `APNS_KEY_ID`: the identifier for that APNs key
- `APPLE_TEAM_ID`: the Apple Developer team identifier
- `APNS_TOPIC`: `com.agenthail.ios`

An App Store Connect API key is not an APNs provider key. Apple rejects it with `InvalidProviderToken`. Create the provider key under Certificates, Identifiers & Profiles, choose Keys, enable Apple Push Notifications service, and download the `.p8` file.

The iOS App ID must have App Attest enabled before Xcode creates its provisioning profile. The Worker derives the App Attest bundle identifier from `APNS_TOPIC`; `APP_ATTEST_BUNDLE_ID` can override it. Development attestations remain rejected unless `APP_ATTEST_ALLOW_DEVELOPMENT=true` is set explicitly outside production.

## Security boundary

The Worker stores an opaque APNs device token, its environment, and the hash of a random per-installation capability. It forwards notification payloads to Apple and does not persist them. A send may carry a `collapseId` of at most 64 bytes, which the Worker passes to Apple as `apns-collapse-id` so a repeated notification replaces the one the device already shows. A send may also carry a `requestId` (letters, digits, `.`, `_` and `-`, at most 120): the Worker passes it through with `mutable-content` so the app's notification service can fetch the request from the paired computer and offer its buttons. The Worker ignores any other request detail a send includes; what a request asks never reaches it.

A registration may carry a `tag` (letters, digits, `.`, `_` and `-`, at most 64), which the Worker stores with the installation and adds as `host` to every notification and withdrawal sent through it; `/health` lists the `host-tag` capability. A client paired with several computers can register once per computer with that computer's host ID as the tag, to tell which computer a notification came from. A sender cannot set or change it: only the registration does.

Registration requires an App Attest statement bound to a fresh, five-minute, single-use challenge stored in a Durable Object. Verification covers the Apple certificate chain, dates, nonce, key and credential identifiers, app identifier, counter, and production environment before a relay credential is issued.

Relay credentials expire after 90 days. Existing credentials without an App Attest record are revoked on use. Registration, credential checks, and delivery use Cloudflare rate limiting. The Worker fails closed when a required store, app identity, or limiter is unavailable.

## Diagnosing a push that did not arrive

The daemon writes one line to `daemon.log` for every notification it sends to paired phones and every withdrawal: `push <eventType> session=<id> request=<id|-> key=<collapseId> devices=N delivered=k failed=m`, followed by each failed device's first eight id characters and the error, and a note when no paired device takes notifications or no relay is configured. A device counts as delivered when the relay answered 2xx, which it does only after APNs accepted the push; that is not proof the phone showed it. The daemon also keeps each device's latest attempt in the registry's `push_outcomes` table: event type, when it was attempted, when a push last succeeded, and the last error and when. `agenthail doctor` lists every device registered for notifications with those times, and fails when a device's last push failed after its last delivery or when devices are paired but no relay is configured.

## Test and deploy

Run `npm test` from `push-relay`.

The tracked Wrangler configuration contains no account or resource IDs. The release workflow finds or creates a KV namespace named `agenthail-push-devices` when no namespace ID is configured, then renders it into the ignored production configuration. Set the Cloudflare account credentials and APNs secrets before using the relay.

Production CI preserves the existing device namespace by exact title or by the optional `CLOUDFLARE_PUSH_DEVICES_KV_ID` secret. The release workflow renders an ignored `wrangler.production.toml` beside the portable configuration. This prevents an upgrade from silently creating an empty namespace and disconnecting paired phones.

The release workflow probes Apple with the configured APNs identity before deploying. A valid identity returns `BadDeviceToken` for the probe's deliberately invalid device token. Authentication, topic, or key errors stop the release.

After the probe passes, the workflow uploads the validated iPhone build, verifies that the exact version and build is ready in the configured automatic internal TestFlight group with testers, syncs the APNs identity to Cloudflare, deploys the locked Worker version, verifies `/health`, and publishes the GitHub release. See [TestFlight management](testflight.md) for group configuration and GitHub management operations.

To roll back, deploy the `push-relay` directory from the last known-good tag with the same secrets, then verify `/health` before restoring or rerunning the native release.
