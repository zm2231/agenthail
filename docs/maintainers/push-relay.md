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

The Worker stores an opaque APNs device token, its environment, and the hash of a random per-installation capability. It forwards notification payloads to Apple and does not persist them. A send may carry a `collapseId` of at most 64 bytes, which the Worker passes to Apple as `apns-collapse-id` so a repeated notification replaces the one the device already shows. A send may also carry a `requestId` (letters, digits, `.`, `_` and `-`, at most 120): the Worker passes it through with `mutable-content` so the app's notification service can fetch the request from the paired computer and offer its buttons. A send may carry a `pushId`, passed through the same way for the app's delivery receipt. The Worker ignores any other request detail a send includes; what a request asks never reaches it.

A registration may carry a `tag` (letters, digits, `.`, `_` and `-`, at most 64), which the Worker stores with the installation and adds as `host` to every notification and withdrawal sent through it; `/health` lists the `host-tag` capability. A client paired with several computers can register once per computer with that computer's host ID as the tag, to tell which computer a notification came from. A sender cannot set or change it: only the registration does.

A send may carry a `dedupeKey` (64 lowercase hex characters), which lets every computer paired to one phone send the same notification while the phone gets it once. The first send for a key claims it in a Durable Object named by the SHA-256 of the registration's device token and the key, and goes to Apple. The claim is pending for 30 seconds while that send is delivered: when Apple accepts it, the claim is confirmed for ten minutes, and when Apple refuses it or the request fails, the claim is released. A later send for the same key and device token answers `{"ok":true,"deduped":true}` and reaches no one once the claim is confirmed, and `503` with `dedupe_pending` while it is pending, so the sender retries and delivers the notification itself if the first send failed. A confirm or release that is lost therefore never hides a notification past the 30-second lease. Because the claim is scoped by device token, registrations from one phone to several computers share it and other phones are unaffected. The object stores only whether the notification was delivered and until when, and an alarm deletes it when that time passes. A send without `dedupeKey` is delivered exactly as before. `/health` lists the `dedupe-key` capability. A malformed key is refused with `invalid_dedupe_key` before anything reaches Apple, and a key with no dedupe store bound fails closed with `dedupe_store_unavailable`.

Registration requires an App Attest statement bound to a fresh, five-minute, single-use challenge stored in a Durable Object. Verification covers the Apple certificate chain, dates, nonce, key and credential identifiers, app identifier, counter, and production environment before a relay credential is issued.

Relay credentials expire after 90 days. Existing credentials without an App Attest record are revoked on use. Registration, credential checks, and delivery use Cloudflare rate limiting. The Worker fails closed when a required store, app identity, or limiter is unavailable.

## Diagnosing a push that did not arrive

The daemon writes one line to `daemon.log` for every notification it sends to paired phones and every withdrawal: `push <eventType> session=<id> request=<id|-> key=<collapseId> devices=N delivered=k failed=m`, followed by each failed device's first eight id characters and the error, and a note when no paired device takes notifications or no relay is configured. A device counts as delivered when the relay answered 2xx, which it does only after APNs accepted the push; that is not proof the phone showed it. The daemon also keeps each device's latest attempt in the registry's `push_outcomes` table: event type, when it was attempted, when a push last succeeded, and the last error and when. `agenthail doctor` lists every device registered for notifications with those times, and fails when a device's last push failed after its last delivery or when devices are paired but no relay is configured.

To tell a push APNs accepted from one that reached the phone, every send carries a random `pushId` (lowercase letters and digits, 8 to 64) for that device. The daemon records each send in `push_sends` before it calls the relay, since the phone's receipt can arrive before the relay answers. A relay with the `push-id` capability passes the id on with `mutable-content` and answers `{"ok":true,"receipt":true}` when the registration carries a host tag; the send is then confirmed and expects a receipt, and the daemon keeps the last 50 confirmed sends per device. Any other answer, including one from a relay without the capability or for a registration without a tag, removes the record without touching the confirmed ones; an unconfirmed record left by a daemon that stopped mid-send is dropped after a day. Forgetting a computer on the phone drops the receipts waiting for it, and a receipt still waiting after a week is dropped. The app's notification service posts `{pushId, receivedAt, alerts}` to `POST /api/v1/device/push-receipts` on the computer named by the payload's `host`, with that computer's device token, while it fetches any request; a receipt it cannot deliver waits as its own item in the keychain group it shares with the app, which sends it when it next comes to the foreground. The daemon accepts a receipt only for a push it sent to the device that sends it. `doctor` then shows when a push last arrived on the phone and whether Agenthail's alerts were on, and fails when two or more pushes accepted more than ten minutes ago, after the last arrival, never arrived. An older relay answers without `receipt`, so no receipt is expected through it, and an older daemon sends no `pushId`, so the relay answers it as before. Deploy the relay first, then release the daemon and app. Arrival is not display: a notification that reached the phone can still be silenced by Focus.

## Test and deploy

Run `npm test` from `push-relay`.

The tracked Wrangler configuration contains no account or resource IDs. The release workflow finds or creates a KV namespace named `agenthail-push-devices` when no namespace ID is configured, then renders it into the ignored production configuration. Set the Cloudflare account credentials and APNs secrets before using the relay.

Production CI preserves the existing device namespace by exact title or by the optional `CLOUDFLARE_PUSH_DEVICES_KV_ID` secret. The release workflow renders an ignored `wrangler.production.toml` beside the portable configuration. This prevents an upgrade from silently creating an empty namespace and disconnecting paired phones.

The release workflow probes Apple with the configured APNs identity before deploying. A valid identity returns `BadDeviceToken` for the probe's deliberately invalid device token. Authentication, topic, or key errors stop the release.

After the probe passes, the workflow uploads the validated iPhone build, verifies that the exact version and build is ready in the configured automatic internal TestFlight group with testers, syncs the APNs identity to Cloudflare, deploys the locked Worker version, verifies `/health`, and publishes the GitHub release. See [TestFlight management](testflight.md) for group configuration and GitHub management operations.

To roll back, deploy the `push-relay` directory from the last known-good tag with the same secrets, then verify `/health` before restoring or rerunning the native release.
