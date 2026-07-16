# Agenthail push relay

This Cloudflare Worker is the narrow APNs delivery boundary for the native iPhone companion. It stores an opaque APNs device token, the sandbox or production environment, and the hash of a random per-installation capability. Notification payloads are forwarded to Apple and are not persisted.

Required secrets:

- `APNS_KEY_P8`: an APNs authentication key from the Apple Developer portal
- `APNS_KEY_ID`: the key identifier for that APNs key
- `APPLE_TEAM_ID`: the Apple Developer team identifier
- `APNS_TOPIC`: `com.agenthail.ios`

The iOS App ID must have the App Attest capability enabled before Xcode can create a valid provisioning profile. The Worker derives the App Attest bundle identifier from `APNS_TOPIC`; `APP_ATTEST_BUNDLE_ID` can override it. Development attestations remain rejected unless `APP_ATTEST_ALLOW_DEVELOPMENT=true` is set explicitly in a non-production environment.

An App Store Connect API key is not an APNs provider key and will be rejected by Apple with `InvalidProviderToken`.

Registration requires an Apple App Attest statement bound to a fresh, five-minute, single-use challenge stored in a Durable Object. The verifier pins Apple's App Attestation root and checks the certificate chain and dates, nonce, key and credential identifiers, app identifier, counter, and production environment before issuing a relay credential. Existing credentials without an App Attest record are revoked on use. Registration and send traffic also use Cloudflare's native Rate Limiting bindings. The Worker fails closed if the challenge store, app identity, or either limiter is missing; it does not fall back to a non-atomic counter.

Run tests with `npm test`. Deploy with `npm run deploy` after configuring the KV binding, rate-limit bindings, and secrets. `/health` reports unhealthy until App Attest, APNs, and both rate limiters are configured, and exposes protocol compatibility, capabilities, and the deployed release version. Relay credentials expire after 90 days; registration returns the expiry so native clients can renew before it. Registration and delivery responses never return an APNs token.

The release workflow tests the relay before building native artifacts. Its final publication job deploys the reviewed relay with the locked Wrangler version, verifies `/health` against the release tag and protocol 2, uploads the validated iPhone build, and only then publishes the draft GitHub release. To roll back, check out the last known-good tag, deploy its `push-relay` directory with the same secrets, then confirm `/health` before rerunning or restoring a native release.
