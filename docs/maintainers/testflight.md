# TestFlight releases and operations

Use this runbook to release the iPhone app, manage internal tester access, and diagnose delivery failures. The [Release workflow](../../.github/workflows/release.yml) builds and uploads the app. The separate [TestFlight management workflow](../../.github/workflows/testflight.yml) operates on existing builds and testers without publishing a Mac release or deploying the relay.

## Release a build

Pushing a `v*` tag triggers Release. A normal push to `main` runs CI; it does **not** upload an IPA. Release also supports manual dispatch with a version:

```sh
export GH_TOKEN="$(~/.config/github-apps/gh-app-token.sh zm2231)"
# Replace this example with the intended new release version.
gh workflow run release.yml --repo zm2231/agenthail --ref main -f version=v0.2.22
```

Release is a combined product release: it builds and validates the Mac package and iOS archive, validates the IPA, creates a draft GitHub release, uploads to TestFlight, verifies internal availability, deploys the relay, and publishes the GitHub release. Use the management workflow for TestFlight-only operations. See [push notification service](push-relay.md) for relay credentials and deployment behavior.

The marketing version comes from the release tag or dispatch input. The iOS build number comes from the Release workflow's `github.run_number`. Read both from the release run or App Store Connect; a management workflow's run number is unrelated to the app build number.

Successful upload or `processingState: VALID` alone does not establish tester access. The release gate requires:

- The exact bundle ID, iOS platform, marketing version, and build number.
- A valid, unexpired processed build.
- The configured group belonging to that app, being internal, and having automatic distribution enabled.
- At least one tester and the exact build assigned to that group.
- Internal state `READY_FOR_BETA_TESTING` or `IN_BETA_TESTING`.

[The gate implementation](../../push-relay/scripts/wait-testflight-build.mjs) shares a default 30-minute wait budget across processing and internal readiness. Missing compliance, invalid processing, expiry, or a failed readiness check stops publication. Upload and subsequent deployment are separate steps: a failed overall release can still have uploaded a build. Inspect the exact build before rerunning a release; rerunning the same GitHub run reuses its build number.

## App, group, and credentials

Initial setup verified September 11, 2026:

| Setting | Value |
| --- | --- |
| App | Agenthail, App Store Connect ID `6791675903` |
| `AGENTHAIL_IOS_BUNDLE_ID` | `com.agenthail.ios` |
| Internal group | Agenthail Internal |
| `AGENTHAIL_TESTFLIGHT_GROUP_ID` | `a6f1cf13-5a4e-42bc-b5c0-3d73d7d9e0cf` |

These identifiers are configuration, not credentials. Check current GitHub repository variables before changing or recreating the group.

The management workflow uses these repository secrets:

| Secret | Purpose |
| --- | --- |
| `APPLE_NOTARY_KEY_BASE64` | Base64-encoded App Store Connect `.p8` private key |
| `APPLE_NOTARY_KEY_ID` | API key identifier |
| `APPLE_NOTARY_ISSUER_ID` | API issuer UUID; do not substitute a team ID |

Despite the `NOTARY` names, these credentials are also used for App Store Connect. They are distinct from the APNs notification key. Keep private keys out of the repository and logs. The workflow creates short-lived JWTs in the runner; operators do not need the Apple private key locally when dispatching through GitHub.

The iOS archive additionally needs `APPLE_TEAM_ID`, `APPLE_IOS_SIGNING_IDENTITIES_P12_BASE64`, `APPLE_IOS_SIGNING_IDENTITIES_P12_PASSWORD`, and `APPLE_IOS_PROVISIONING_PROFILE_BASE64`. The combined release also needs its Mac signing and relay configuration; the [release workflow](../../.github/workflows/release.yml) is the authoritative list. After rotating credentials, run a read-only management status check. Signing/profile changes also require archive validation; a status check cannot validate signing assets.

## Set up internal access

1. Open Agenthail → TestFlight in App Store Connect using the intended Apple team.
2. Create an internal group with **Enable automatic distribution** selected. The configured group currently shows **Automatic for Xcode Builds** in Settings. If a replacement group is needed, create it with the intended distribution setting and update the repository variable to its exact ID.
3. Add eligible existing App Store Connect users with access to this app. First-time account/user setup and role assignment happen in App Store Connect; the management script does not grant roles or create accounts.
4. Ensure the existing build appears in the group's Builds tab. Automatic distribution handles future uploads; use `assign-build` for an existing build when Apple permits assignment.
5. Have the tester accept the invitation in TestFlight using the intended Apple Account. Verify the installed version/build on the device or the tester's **Installed** entry in App Store Connect.

An existing tester must already be associated with this app for `invite-existing-tester` to resolve it. That action adds group membership; it does not bootstrap a new tester or guarantee an invitation resend. Use `resend-invitation` for an existing group member. See [Apple's internal tester instructions](https://developer.apple.com/help/app-store-connect/test-a-beta-version/add-internal-testers).

## Run management operations

Open [Actions → TestFlight management](https://github.com/zm2231/agenthail/actions/workflows/testflight.yml), choose **Run workflow**, use `main`, and supply the action's inputs. The same operations are available through GitHub CLI:

```sh
export GH_TOKEN="$(~/.config/github-apps/gh-app-token.sh zm2231)"
# Read-only overview.
gh workflow run testflight.yml --repo zm2231/agenthail --ref main -f action=status
# Exact readiness check; use the version/build you intend to inspect.
gh workflow run testflight.yml --repo zm2231/agenthail --ref main \
  -f action=status -f version=0.2.21 -f build=32
# Inspect the dispatched run, then view its result/logs using the returned ID.
gh run list --repo zm2231/agenthail --workflow testflight.yml --limit 5
gh run view RUN_ID --repo zm2231/agenthail --log
```

Dispatch acceptance only means GitHub queued the workflow. Check its completed result and output.

| Action | Required inputs | Effect |
| --- | --- | --- |
| `status` | None, or both `version` and `build` | Reports group configuration, counts, build states, and available invitation-state aggregates. Exact status waits through the release gate, then reads a fresh snapshot. |
| `assign-build` | `version`, `build` | Adds the exact existing build to the configured internal group. Already assigned is a no-op. |
| `invite-existing-tester` | `email` | Adds an existing app-associated tester to the group. Already assigned is a no-op. Ambiguous matches are rejected. |
| `resend-invitation` | `email` | Requests one email for an existing group member. Apple's `ALREADY_ACCEPTED` response is a successful no-op. |
| `remove-tester` | `email` | Removes membership in this group. Already absent is a no-op; this does not delete the Apple account or remove other group memberships. |
| `notes` | `version`, `build`, `notes`; optional `locale` | Creates or updates localized What to Test text, then reads it back. Locale defaults to `en-US`. |
| `expire-build` | `version`, `build` | Expires the selected build. Treat expiry as retirement, not a reversible pause; distribute another valid build when retiring one. |

For example, updating notes uses the same command with `-f action=notes -f version=0.2.21 -f build=32 -f locale=en-US -f notes='Test pairing and session timelines.'`. Tester actions use `-f email=tester@example.com`; substitute the intended tester. Run mutations individually and inspect the result before repeating them.

[The management script](../../push-relay/scripts/manage-testflight.mjs) also runs locally under Node.js with the same five environment variables listed above. CLI selectors use `--version`, `--build`, `--email`, `--locale`, and `--notes` instead of workflow inputs. It emits a JSON result on stdout and errors/progress on stderr. Its CLI additionally supports `remove-build --version VERSION --build BUILD`; that action is not exposed in the workflow dropdown.

Mutations issue one write and read back the resulting resource or membership. They do not blindly retry uncertain writes. Invitation acceptance and inbox delivery are distinct: `requested: true` confirms the API request, while `already: true, state: ACCEPTED` means Apple reports prior acceptance. Neither alone proves the latest build is installed.

Status JSON omits tester email addresses. GitHub dispatch inputs, step environment output, and some lookup errors can contain the supplied email; workflow logs are not an anonymous tester report. API keys and authorization headers must remain secret.

## Interpret status and recover failures

| Observation | Next step |
| --- | --- |
| `groupReady: true` without an exact build check | This confirms app/group identity and internal type. Check `autodistribution`, tester count, and an exact version/build before asserting delivery. |
| `invitationStates: {"UNKNOWN": ...}` | Apple did not supply that state. Do not interpret it as uninvited or uninstalled; inspect the tester in App Store Connect/TestFlight. |
| Upload valid but no build on the phone | Run exact status; check compliance, group membership, tester membership, and the phone's Apple Account. Refresh the UI and inspect the installed build number. |
| `MISSING_EXPORT_COMPLIANCE` | Review the app's encryption use and complete the applicable Apple declaration. Do not blindly choose an exemption for changed crypto behavior. |
| `401` or `403` | Check API key validity, issuer, role/app access, and the failing endpoint. An unsupported relationship operation can also return `403`; it does not necessarily mean the key needs broader permissions. |
| `400` naming filters or parameters | Check supported API query combinations. The script resolves group members through the group's tester endpoint and verifies app membership, avoiding combined tester-filter assumptions. |
| Duplicate tester records for one email | Identify the intended app and group. Resend/removal match within the configured group; remaining ambiguity stops the action. Do not choose the first record. |
| `ALREADY_ACCEPTED` on resend | Access was already accepted. The script reports a successful no-op; check TestFlight rather than repeatedly sending invitations. |
| Timeout, network error, or readback mismatch after a mutation | Inspect App Store Connect first. The write may have succeeded even when confirmation failed. |
| Build expired | Select or upload a different valid build; do not expect assignment or resending an invitation to revive it. |

The iOS app declares `ITSAppUsesNonExemptEncryption: false` in both [XcodeGen configuration](../../native/project.yml) and [Info.plist](../../native/iOS/Info.plist). Release verifies the value in the archived app. The declaration was based on the inspected Apple-provided networking/cryptography; reassess it if app encryption or linked libraries change. See [Apple's encryption guidance](https://developer.apple.com/documentation/security/complying-with-encryption-export-regulations).

Apple agreements, two-factor authentication, first-time account/role setup, external tester onboarding/review, public links, and App Store submission are outside this internal-group workflow. Complete those through the applicable Apple flow. No workflow here accepts agreements, grants privileges, or bypasses beta review.

## Verification and maintenance

After changing management scripts or workflows:

```sh
npm test --prefix push-relay
git diff --check
# After changing native metadata:
scripts/test-xcodegen-project.sh
```

Validate workflow YAML with `actionlint`, and build/archive the iOS app when changing signing or metadata. Then dispatch an exact read-only status check using the saved GitHub credentials. Unit tests exercise failed, ambiguous, duplicate, and uncertain responses; they cannot replace an Apple API check. Do not expire a live build or remove a real tester just to exercise a test.

Initial delivery receipts, September 11, 2026:

- [Release 0.2.21 / build 32](https://github.com/zm2231/agenthail/actions/runs/34582379701): completed upload and combined release.
- [Exact internal readiness](https://github.com/zm2231/agenthail/actions/runs/34653710399): automatic group, tester presence, exact build assignment, and `IN_BETA_TESTING` confirmed.
- [Release notes write/readback](https://github.com/zm2231/agenthail/actions/runs/34653091643): completed successfully.
- [Accepted invitation no-op](https://github.com/zm2231/agenthail/actions/runs/34653569420): Apple confirmed prior acceptance.

These are historical receipts, not a current health monitor. App Store Connect also displayed build 0.2.21 (32) installed on the tester's iPhone during setup. No new IPA was uploaded solely for the management changes; future releases carry the tracked encryption metadata declaration.
