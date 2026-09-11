# TestFlight management

The manual **TestFlight management** workflow provides a small, bounded App Store Connect operator path. It calls `push-relay/scripts/manage-testflight.mjs` with an App Store Connect API key and uses the existing internal beta group configured in the repository variable `AGENTHAIL_TESTFLIGHT_GROUP_ID`.

Supported operations are:

- `status`: verify the configured group belongs to the configured bundle ID, is internal, and report group readiness, autodistribution, per-build internal/external beta states, build count, and tester count. Supplying both an exact version and build also reports whether that build is assigned and internally ready. Tester email addresses are never printed.
- `assign-build`: attach the exact marketing version and build number to the existing group.
- `invite-existing-tester`: add one existing tester selected by exact email to the existing internal group. The name describes group assignment; it does not promise an invitation resend, and an already assigned tester is a read-only idempotent result.
- `notes`: set localized TestFlight “What’s New” text for the exact version and build.
- `remove-tester`: remove one existing tester selected by exact email from the existing internal group.
- `expire-build`: expire the exact version and build. This lifecycle action is available only through manual dispatch.

It does not create apps, groups, testers, users, roles, public links, recruitment criteria, or security privileges. It does not send arbitrary invitations or select testers by fuzzy matching. A duplicate assignment or removal is reported as an idempotent result; mutations perform one request and then read back the relationship or resource. There is no blind retry.

Configure these GitHub values before using the workflow:

- Secrets: `APPLE_NOTARY_KEY_BASE64`, `APPLE_NOTARY_KEY_ID`, and `APPLE_NOTARY_ISSUER_ID`.
- Repository variables: `AGENTHAIL_IOS_BUNDLE_ID` and `AGENTHAIL_TESTFLIGHT_GROUP_ID`.

The API key must have the App Store Connect access required by the selected operation. A `403` is surfaced as an error. The script bounds pagination to 20 pages of up to 200 resources and accepts a `next` link only when it has the same origin as the configured API endpoint. API response bodies and authorization headers are not logged.

The normal release path is tag or manual release dispatch, archive and export the iOS app, validate the IPA, upload it to TestFlight, then wait for the exact version/build to reach the configured automatic internal group before the release is published. The status action with both version and build runs that same `waitForInternalBuild` gate as a live read-only check. The iOS metadata declares the app’s non-exempt encryption state for the release workflow; any remaining App Store Connect metadata, agreements, or acceptance decisions stay with the maintainer.

This automation does not accept Apple agreements, complete two-factor authentication, perform external beta review, or decide whether a build is ready for human acceptance. Those steps remain with the maintainer in App Store Connect. Apple documents the [App Store Connect API](https://developer.apple.com/documentation/appstoreconnectapi/), [beta group build relationships](https://developer.apple.com/documentation/appstoreconnectapi/post-v1-betagroups-_id_-relationships-builds), [beta tester relationships](https://developer.apple.com/documentation/appstoreconnectapi/post-v1-betagroups-_id_-relationships-betatesters), and [beta build localizations](https://developer.apple.com/documentation/appstoreconnectapi/beta-build-localizations).
