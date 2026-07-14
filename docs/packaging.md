# macOS package architecture

Agenthail's primary installer is a signed and notarized Apple silicon `.pkg`. The package is self-contained for the target Mac and does not depend on Homebrew, Xcode Command Line Tools, Go, Swift, Node.js, or Python already being installed.

## Runtime decision

The Claude and Notion adapters require Chrome-compatible TLS behavior from `curl_cffi` and a local cookie reader. Replacing either with a different networking stack would change observable authentication and transport behavior. The package therefore bundles pinned runtimes instead of rewriting those adapters during the packaging change:

- CPython 3.13 from `python-build-standalone`
- Node.js 22 LTS from the official Node distribution
- hash-locked Python wheels from `packaging/runtime-requirements.txt`
- npm dependencies locked by `sidecar/package-lock.json`

The build verifies the runtime archive hashes before extraction. `THIRD_PARTY_NOTICES.md` records sources and licenses, and the licenses shipped by each dependency remain in the installed payload.

## Installed layout

| Path | Purpose |
|---|---|
| `/Library/Application Support/Agenthail` | CLI binary, bundled runtimes, sidecars, skills, licenses |
| `/Applications/Agenthail.app` | Native menu bar companion and notifications |
| `/usr/local/bin/agenthail` | Launcher that binds the bundled runtime explicitly |
| `/usr/local/bin/agenthail-uninstall` | Complete package removal command |

The installer migrates an existing Homebrew Agenthail installation before activating the package. It preserves `~/.agenthail` and relinks only Agenthail-managed skill symlinks. The postinstall script installs the per-user launchd daemon, registers the companion login item, and opens the companion for the logged-in console user. A headless or MDM install leaves activation to `agenthail daemon install` under the intended user account.

## Build and verification

```bash
AGENTHAIL_VERSION=v0.1.6 \
AGENTHAIL_NOTARY_PROFILE=agenthail-notary \
scripts/build-pkg.sh

scripts/test-pkg-artifact.sh dist/Agenthail-v0.1.6-arm64.pkg
```

Production builds fail closed without Developer ID Application, Developer ID Installer, and notary credentials. Local package development can use `AGENTHAIL_ALLOW_DIRTY=1 AGENTHAIL_ALLOW_UNNOTARIZED=1`.

The release workflow expects these repository secrets:

- `APPLE_DEVELOPER_ID_APPLICATION_P12_BASE64`
- `APPLE_DEVELOPER_ID_INSTALLER_P12_BASE64`
- `APPLE_DEVELOPER_ID_P12_PASSWORD`
- `APPLE_NOTARY_KEY_BASE64`
- `APPLE_NOTARY_KEY_ID`
- `APPLE_NOTARY_ISSUER_ID`

Export the Developer ID Application and Developer ID Installer identities as separate `.p12` files using the same password. Store each file as base64 in its matching secret and store their shared export password in `APPLE_DEVELOPER_ID_P12_PASSWORD`.

The `.pkg` verifier expands the real artifact, checks signatures and expected files, runs the embedded CLI and both runtimes with a restricted `PATH`, rejects Homebrew-linked Mach-O files, and rejects AppleDouble metadata.

The release runner then installs the real package, verifies the supervised daemon, dashboard, skills, and doctor output, installs the same package again to exercise upgrade and daemon replacement, and runs the packaged uninstaller. Publication does not run until that lifecycle passes.

## Uninstall

```bash
sudo agenthail-uninstall
```

This removes the application, package payload, launchd service, login item, command launchers, package receipt, and Agenthail-managed skill links. It preserves `~/.agenthail` by default. Use `sudo agenthail-uninstall --purge-data` to remove local registry, queue, history, logs, and dashboard configuration too.
