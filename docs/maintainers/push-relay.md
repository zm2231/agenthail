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

The Worker stores an opaque APNs device token, its environment, and the hash of a random per-installation capability. It forwards notification payloads to Apple and does not persist them.

Registration requires an App Attest statement bound to a fresh, five-minute, single-use challenge stored in a Durable Object. Verification covers the Apple certificate chain, dates, nonce, key and credential identifiers, app identifier, counter, and production environment before a relay credential is issued.

Relay credentials expire after 90 days. Existing credentials without an App Attest record are revoked on use. Registration, credential checks, and delivery use Cloudflare rate limiting. The Worker fails closed when a required store, app identity, or limiter is unavailable.

## Test and deploy

Run `npm test` from `push-relay`.

The tracked Wrangler configuration contains no account or resource IDs. The release workflow finds or creates a KV namespace named `agenthail-push-devices` when no namespace ID is configured, then renders it into the ignored production configuration. Set the Cloudflare account credentials and APNs secrets before using the relay.

Production CI preserves the existing device namespace by exact title or by the optional `CLOUDFLARE_PUSH_DEVICES_KV_ID` secret. The release workflow renders an ignored `wrangler.production.toml` beside the portable configuration. This prevents an upgrade from silently creating an empty namespace and disconnecting paired phones.

The release workflow probes Apple with the configured APNs identity before deploying. A valid identity returns `BadDeviceToken` for the probe's deliberately invalid device token. Authentication, topic, or key errors stop the release.

After the probe passes, the workflow uploads the validated iPhone build, waits for TestFlight to accept the exact version and build, syncs the APNs identity to Cloudflare, deploys the locked Worker version, verifies `/health`, and publishes the GitHub release.

To roll back, deploy the `push-relay` directory from the last known-good tag with the same secrets, then verify `/health` before restoring or rerunning the native release.
