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
so a device whose slimming failed is never found and reused. The daemon and the
script both find or create a device only while holding
`/tmp/agenthail-simulator-create.lock`, so two first users on a Mac create one
device between them.

## Devices for an older runtime

`acquire` with a `runtime`, such as `18.5`, leases a device on that installed
iOS runtime instead, for an app that does not run on the newest one. A
`runtime` is an installed version (`18.5`, `iOS 18.5`), a shorter version that
names only one installed runtime (`18`), or a runtime identifier. The daemon
picks the device this way:

- the shared device of that kind, when it is on that runtime;
- otherwise one device per kind and runtime, named like
  `Agenthail iPhone (iOS 18.5)`, created on the first request for it and
  shared from then on. When the newest runtime is asked for and the kind has no
  shared device yet, the shared device is created instead.

It is created the same way as a shared device, under the same lock and slimmed
before it takes its name, and its leases end and clean up the same way. A
runtime that is not installed, or a shorter version that names more than one,
is refused as `invalid`, naming the installed versions (also in `installed`
for the Mac's own sessions; a forwarded refusal carries only its message);
Agenthail never downloads a runtime. It is checked before any lease, so the
answer never depends on what is held. A new device is created as the first
iPhone or 11-inch iPad Pro the runtime itself supports, since an older runtime
does not run the newest models. A simulator that already has the device's name but
is on another runtime is refused as `conflict`, to be renamed or deleted by
hand. Without `runtime`, `acquire` leases the shared device as it always has.

A session may hold the shared device and a device for a runtime of the same
kind at once; then every call names the one it means with `runtime` as well
as `kind`, and `release` with a `kind` that names both is refused. `release`
with neither `kind` nor `runtime` ends every lease the session holds. `list` shows every Agenthail device with its runtime, and the
installed runtimes in `runtimes`; `agenthail doctor` (not the daemon's
minute-by-minute status probe) prints each device and
its runtime under its `simulators:` line, and `simulators.devices` in
`--json`. `scripts/ios-simulator.sh iphone --runtime 18.5 run ...` finds or
creates the same device for a test run.

## Leases

A session drives a device only while it holds its lease:

- `acquire` with a `kind` boots the device and takes its lease. The lease holds
  an exclusive `flock` on `/tmp/agenthail-simulator-<udid>.lock`, the lock
  `scripts/ios-simulator.sh run` takes through `lockf`, so a lease and a script
  test run never share a device. Another session's lease, or a script run,
  answers `busy` at once; nothing waits.
- One session may hold both kinds, and devices for more than one runtime;
  then each call names its `kind`, and its `runtime` when that is needed to
  tell them apart.
- A lease ends 10 minutes after its last call, 60 minutes after it began unless
  `renew` moves that on, at `release`, and when its session is archived. The
  daemon checks every 30 seconds. A daemon that exits releases every lock with
  it.
- Ending a lease terminates the apps it launched and clears the device's
  location and status bar overrides. It never erases the device, changes its
  appearance or touches apps it did not launch. An action still running when
  the lease ends finishes first, so an app it launches is terminated too, and
  no action starts on an ended lease.
- A session's simulator calls run one at a time. Archiving releases the
  session's leases in the background, after any call still running for it has
  finished, and before a restored session's next call starts; the cleanup holds
  the device's lock until it is done. The session's output folder is removed
  after that only if the session holds no lease by then.
- Sessions run as the same user, so leases and output folders keep them apart
  by convention, as the browser host's tabs do: a session with a shell can run
  `simctl` itself.

## Actions

Every action except `list`, `acquire`, `renew` and `release` needs a lease.

- `describe` reads the accessibility tree with `axe describe-ui` and returns one
  line per element that has a label, identifier, value or VoiceOver action,
  each with a ref, its type and its frame in points, plus its traits and
  VoiceOver actions when it has any. A value that axe reports as a number or boolean shows as written
  (`12`, `true`). The whole tree is written to a file; the result
  returns as much as fits the tool result limit, with `truncated` and the
  file. Refs are valid until the next `describe`.
- `tap` takes a ref (its frame's centre) or `x` and `y`; `swipe` takes a ref
  or `x`, `y` and `toX`, `toY`, with `seconds` (0.6 by default). `type` sends
  its text to the focused field on AXe's standard input; it cannot send
  keys such as Tab or the arrows. `key` presses hardware keyboard keys by
  HID keycode (0 to 255): `key` alone (`axe key`), `key` while holding
  `modifiers` (`axe key-combo`; distinct modifier keys 224 to 231, so `key`
  43 with `modifiers` `[225]` is Shift-Tab), or `keys`, up to 100 pressed in
  order (`axe key-sequence`). `button` presses
  `home`, `lock` or `side-button`. There is no Siri, Apple Pay, microphone or
  voice action.
- `install` takes a `.app` folder inside the session's `outDir` (in the lease
  and `list`); any other path, including another session's folder or one
  reached through a link, is `out_of_scope`. The daemon copies the app through
  an `os.Root` opened on that folder, refusing an app that holds a link, into a
  private staging folder, and installs that copy, so the path cannot be
  swapped between the check and the install. `launch` passes `args`, and `env` as
  `SIMCTL_CHILD_` variables, and restarts an app already running.
  `uninstall` removes the app with `bundleId` from the leased device.
  `terminate` and `openUrl` do what they say.
- `screenshot` writes a PNG of the screen and returns `file`, `bytes`, `width`
  and `height`; with `path` the daemon copies it there under the same rules as
  a browser screenshot ([browser host](browser-host.md#tabs)).
- `push` sends `payload`, a JSON object with `aps`, with `simctl push`.
  `location` sets `latitude` and `longitude`, or clears them.
- `logs` shows the app's process log for the last `seconds` (at most 300, 60 by
  default), its tail fitted to the result and all of it in a file.

## From a host without simulators

A daemon that runs no simulators, such as one on Linux, forwards each
`agenthail_simulator` call to a paired Mac that does
([paired hosts](hosts.md)):

- A daemon that can run simulators says so in `GET /api/v1/version`
  (`simulators`). The version never waits on a probe: it leaves the field out
  until the first probe finishes, then answers from the last status, and a
  status a minute old starts one background probe that every caller shares. A
  probe that runs out of time leaves the last status in place. A paired host
  reads it each time it connects, and again through the host-only
  `GET /api/v1/simulator/status`, which does wait for a fresh probe, when it
  picks a Mac and what it last read is a minute old or more.
- The Mac is the one named by `simulatorHost` in the forwarding host's
  `~/.agenthail/dashboard.json`, read on every call; when it is unset, the only
  connected paired host that runs simulators. A `dashboard.json` that can't be
  read refuses the call rather than picking a Mac by itself. With none, or several and no
  choice, the call is refused as `unavailable` and says why.
- The call goes to the Mac's host-only `POST /api/v1/simulator`
  (`{owner, request}`). Only a device token with the `host` scope bound to the
  calling host's id is accepted, never a session's token; the Mac holds the
  lease for `<calling host id>::<session id>`, so a session on another host
  never shares a lease with one of the Mac's own sessions.
- A result's file (a screenshot, a describe tree, a log) comes back in the
  answer, at most 8 MiB, and is written on the forwarding host under its own
  output folder for the session, where `path` then copies a screenshot. Results
  name the Mac as `host` and carry no `outDir`.
- A forwarded `install` uploads the app. `app` is an absolute `.app` folder in
  the session's folders on the forwarding host (its ceiling's roots, or its
  working directory), checked as a screenshot `path` is; anything else is
  `out_of_scope`. The forwarding host walks it through an `os.Root`, refusing a
  link or anything that is not a file or folder, and refuses an app whose files
  total over 512 MiB as `too_large` before sending anything. It streams the app
  as a gzip tar holding one `<Name>.app/` folder to the Mac's host-only
  `POST /api/v1/simulator/install?owner=&kind=&runtime=` with
  `Expect: 100-continue`. The Mac checks the lease before it reads the body, so
  a refused install (`not_held`, or `busy` while another upload to the same
  lease runs) sends no app. It unpacks only clean relative paths of files and
  folders under that one `.app`, through an `os.Root`, into
  `~/.agenthail/simulator/out/.staging/install-*`, refusing more than 512 MiB of
  files or of compressed stream. It checks the lease again, installs that copy
  and removes it however the install ends; a daemon that starts removes any
  `install-*` copy a stopped one left. The result names the forwarding host's
  path as `installed`.
- Archiving the session on the forwarding host releases what it holds on every
  Mac it has called, after any forwarded call still running has finished, and
  removes its folder there. A lease the Mac never hears released ends
  when it is idle or reaches its limit.

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
