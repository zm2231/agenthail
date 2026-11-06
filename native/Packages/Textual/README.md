# Textual (vendored)

This is a copy of [Textual](https://github.com/gonzalezreal/textual) 0.5.0, commit
`01b51875a5406eefc95f52a058cb059e7bc94dc4`, under its MIT license (`LICENSE`).

Only `Package.swift`, `Package.resolved`, the licenses, `Sources` and `Tests` are kept. Changes
made here are ordinary commits on top of the upstream copy, so `git log` on this directory lists them.

Run the package tests from this directory. The snapshot tests only build for iOS:

```
xcodebuild test -scheme textual -destination 'platform=macOS'
xcodebuild test -scheme textual -destination 'platform=iOS Simulator,name=<simulator>'
```
