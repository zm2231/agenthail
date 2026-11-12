# Simulator host

On macOS the daemon lets agent sessions drive iOS simulators through
`agenthail_simulator` ([MCP tools](mcp.md)). It runs `xcrun simctl` for
devices, apps, push, location and logs, and [AXe](https://github.com/cameroncooke/AXe)
to read the screen and to tap, type, swipe and press buttons. The code is in
`internal/simhost`; the daemon creates one host at start and the tool calls it
with the calling session as the owner.

## What a host needs

The host can run simulators when all of these hold, checked at most once a
minute:

- it is macOS;
- `xcrun simctl` answers (Xcode is installed and selected);
- an iOS Simulator runtime is installed;
- `axe` is on the daemon's `PATH`. The package does not ship AXe; the person
  installs it with `brew install cameroncooke/axe/axe`.

Until then every call is refused as `unavailable`, with what is missing and its
fix. `agenthail doctor` prints the same as its `simulators:` line (`simulators`
in `--json`). [simslim](https://github.com/MobAI-App/simslim) is optional: with
it, a device the daemon creates is slimmed, which cuts a simulator's memory to
roughly a quarter.

## The shared devices

There are two devices, one per kind, shared by every session and by test runs
through `scripts/ios-simulator.sh`: `iphone` is the simulator named
`Agenthail iPhone` and `ipad` is `Agenthail iPad`. The daemon never creates a
device per run. When a kind's device is missing, the first `acquire` creates it
the way the script does: on the newest iOS runtime, as an iPhone or an 11-inch
iPad Pro, and, when simslim is installed, slimmed with the profile in
`internal/simhost/simslim-profile.json` (a copy of
`scripts/simslim-profile.json`, kept equal by a test) before it takes its name,
so a device whose slimming failed is never found and reused.

## Leases

A session drives a device only while it holds its lease:

- `acquire` with a `kind` boots the device and takes its lease. The lease holds
  an exclusive `flock` on `/tmp/agenthail-simulator-<udid>.lock`, the lock
  `scripts/ios-simulator.sh run` takes through `lockf`, so a lease and a script
  test run never share a device. Another session's lease, or a script run,
  answers `busy` at once; nothing waits.
- One session may hold both kinds; then each call names its `kind`.
- A lease ends 10 minutes after its last call, 60 minutes after it began unless
  `renew` moves that on, at `release`, and when its session is archived. The
  daemon checks every 30 seconds. A daemon that exits releases every lock with
  it.
- Ending a lease terminates the apps it launched and clears the device's
  location and status bar overrides. It never erases the device, changes its
  appearance or touches apps it did not launch.

## Actions

Every action except `list`, `acquire`, `renew` and `release` needs a lease.

- `describe` reads the accessibility tree with `axe describe-ui` and returns one
  line per element that has a label, identifier or value, each with a ref, its
  type and frame in points. The whole tree is written to a file; the result
  returns as much as fits the tool result limit, with `truncated` and the
  file. Refs are valid until the next `describe`.
- `tap` takes a ref (its frame's centre) or `x` and `y`; `swipe` takes a ref
  or `x`, `y` and `toX`, `toY`, with `seconds` (0.6 by default). `type` sends
  its text to the focused field on AXe's standard input. `button` presses
  `home`, `lock` or `side-button`. There is no Siri, Apple Pay, microphone or
  voice action: a microphone prompt on the Mac aborts simulator calls.
- `install` takes a `.app` folder inside the session's `outDir` (in the lease
  and `list`), symlinks resolved; any other path, including another session's
  folder, is `out_of_scope`. `launch` passes `args`, and `env` as
  `SIMCTL_CHILD_` variables, and restarts an app already running.
  `terminate` and `openUrl` do what they say.
- `screenshot` writes a PNG of the screen and returns `file`, `bytes`, `width`
  and `height`; with `path` the daemon copies it there under the same rules as
  a browser screenshot ([browser host](browser-host.md#tabs)).
- `push` sends `payload`, a JSON object with `aps`, with `simctl push`.
  `location` sets `latitude` and `longitude`, or clears them.
- `logs` shows the app's process log for the last `seconds` (at most 300, 60 by
  default), its tail fitted to the result and all of it in a file.

## Output files

Results that write files go under `~/.agenthail/simulator/out/`, in one folder
per owner named by a hash of the owner id (mode 0700, files 0600). Each owner
keeps at most 64 MiB of plain files, oldest removed first; folders, such as an
app copied in, are the owner's to remove. Archiving the session removes its
folder.

## Tests

`internal/simhost` and the daemon's tool tests run against a fake command
runner, so no test starts a simulator, Xcode or AXe. A live check drives the
real devices on a Mac.
