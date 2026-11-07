# iPhone live harness

The iPhone UI tests run against a fake daemon built into the app. `cmd/ios-live-harness` runs real Agenthail daemons for tests that go through the app's real connection path instead: pairing, the event stream, a host going down and coming back.

```
go build -o "$RUNNER_TEMP/agenthail" ./cmd/agenthail
go run ./cmd/ios-live-harness -agenthail "$RUNNER_TEMP/agenthail" -hosts 2 -- <test command> [args...]
```

The harness starts the hosts, runs the test command, stops the hosts and deletes their directories. It exits with the test command's status. It exits with 3 if a host broke its isolation, and with 4 if the hosts could not be set up.

## Hosts

Each host gets a new directory under the system temp directory, or under `/tmp` when the temp directory's path is too long. It must be on the internal disk. The directory path must stay under 60 bytes, because macOS limits a Unix socket path to 104 bytes. Before the daemon starts, the harness writes into the host's registry:

- browser sign-in turned off for Claude and Notion;
- the host name `live-a`, `live-b` and so on;
- `dashboard.json` serving on `127.0.0.1` at a free port;
- two ended Claude conversations, with their transcripts. They show under the `recent` and `all` scopes. The second one's folder is a git repository with an ignored `build.log`, so a file list that came from git leaves that file out.

It writes no Claude session records with process IDs, so the daemon has no live process to attach to, until a test opens a turn through the control port.

The daemon runs as `agenthail daemon-run` in the foreground. No install, service or dashboard command runs. Its environment is the following list and nothing else:

| Variable | Value |
|---|---|
| `PATH` | `<dir>/bin` |
| `HOME`, `CFFIXED_USER_HOME` | `<dir>/home` |
| `TMPDIR` | `<dir>/tmp/` |
| `XDG_RUNTIME_DIR` | `<dir>/run` |
| `CODEX_HOME` | `<dir>/home/.codex` |
| `CLAUDE_CONFIG_DIR` | `<dir>/home/.claude` |
| `AGENTHAIL_CLAUDE_SOCKET_DIR` | `<dir>/sockets` |
| `AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT` | the trap port |
| `AGENTHAIL_PUSH_RELAY_URL` | `http://127.0.0.1:<the trap port>` |
| `LANG` | `en_US.UTF-8` |

`<dir>/bin` holds links to `git`, `kill`, `ps` and `sleep`, the commands the daemon runs by name, and nothing else. No Claude or Codex CLI is on it, so a host cannot find the machine's own.

The trap port is a loopback port the harness holds for the whole run and closes every connection on. No other service on the machine can take it, so a host that looks for Codex or sends a notification reaches nothing.

## Isolation check

From the moment the first host starts until the last one has stopped, the harness reads every open descriptor of each host's daemon and the daemon's child processes, every 250 ms. On Linux it reads `/proc` and `ss`; on macOS it reads `lsof`. The run fails when any of these appears:

- a TCP listener on any port except the host's own dashboard port, or a TCP connection that is neither to or from that port nor to the trap port, on loopback too;
- a TCP peer off this machine, or any UDP socket;
- a Unix socket, bound or connected to, whose path is outside the host's directory;
- an open file under the HOME of the user running the harness, or under `/tmp/cc-socks`, which real Claude sessions share;
- a process running any program other than the agenthail binary, the commands in `<dir>/bin`, the helpers git runs from `git --exec-path`, and on macOS the developer-directory copies of those commands that `xcrun --find` names, which the `/usr/bin` shims run.

Each violation is printed when it is first seen and again at exit. If the harness cannot read the processes, that counts as a failure.

So that a clean run shows the check looked, the harness also prints each distinct descriptor the first time it sees a host hold it, as `ios-live-harness: saw <host>: <descriptor>`. A client's ephemeral port is left out, and files under system directories such as `/usr/lib/` are listed once per directory. At exit it prints how many samples it took. A run that would otherwise pass fails with exit 3 if the watcher took no samples, or never saw a host's own dashboard listener. When the test command or the setup already failed, that failure's status stands, and the reason is printed beside it. An isolation violation still exits 3 whatever else happened.

A host's own daemon may never run `git` or `sleep` while the watcher looks, because each run lasts milliseconds. So once the hosts are up, the harness runs each of them from every host's `<dir>/bin`, with the host's environment, in a form that waits: `git hash-object --stdin` and `sleep 30`. It treats each one as part of that host until the watcher has seen its program, then stops it. On macOS this checks the `/usr/bin` shims and the developer-directory copies they run. If the watcher never sees one of them, the run fails as if the watcher saw nothing.

The probes run whichever `git` the machine's `PATH` resolves, which on the self-hosted runner is Homebrew's. On a Mac without Homebrew git, a `git` program the allow list misses shows as a violation naming its path, and adding that path fixes it.

What the check cannot see:

- A connection, file or child process that opens and closes between two reads.
- A process that detaches from the daemon's process tree, for example by forking twice, is no longer read.
- A connected Unix socket whose peer has no path. Its other end cannot be checked against the host's directory.

## Control port

The test command gets the control address in `AGENTHAIL_LIVE_CONTROL`. It also gets it in `TEST_RUNNER_AGENTHAIL_LIVE_CONTROL`, which `xcodebuild` passes to the UI test runner as `AGENTHAIL_LIVE_CONTROL`. The port is on loopback; `-control 127.0.0.1:<port>` fixes it. The commands are:

| Request | Result |
|---|---|
| `GET /hosts` | `[{"index", "name", "endpoint", "running"}]` |
| `POST /hosts/{i}/link` | `{"url": "agenthail://pair?..."}`: a new pairing for host `i`, valid for 10 minutes |
| `POST /hosts/{i}/reply` with `{"conversation": n, "text": "..."}` | appends an assistant reply to host `i`'s seeded conversation `n`, as Claude would, and answers `{"session": id}` |
| `POST /hosts/{i}/turn/start` with `{"text": "..."}` | opens a turn in a running Claude conversation on host `i` and answers `{"session": id}`. A `sleep` the harness starts from `<dir>/bin` stands in for the Claude process, with a socket in `<dir>/sockets` and a session record saying `busy`; the transcript ends on the prompt. One turn at a time per host; a second answers 409 |
| `POST /hosts/{i}/turn/finish` with `{"text": "..."}` | ends that turn as Claude does: the reply with `end_turn`, a `turn_duration` record, then the session record going `idle` |
| `POST /hosts/{i}/stop` | stops host `i`'s daemon by the process ID the harness recorded |
| `POST /hosts/{i}/start` | starts it again on the same port and waits until it answers |

Once the test command exits, the harness refuses new commands and closes the control port, so a later command fails. Commands already running finish before it stops the hosts. It then stops each open turn's process by the process ID it recorded and removes its socket and session record.

The harness mints a pairing link in the host's registry rather than through `/api/v1/pairings`, which pairs only through a Tailscale address. The link's endpoint is the host's `http://127.0.0.1:<port>`. The phone completes pairing through `POST /api/v1/pair`, as it does in production.

## Tests

`go test ./cmd/ios-live-harness` starts helper processes that stay inside a host's scope, and others that reach a loopback port, a Unix socket and a file outside it. It checks the watcher flags exactly the second set. It also opens and finishes a turn through the control port and reads it with the daemon's own Claude adapter: busy with an active turn while open, idle with the reply once finished. It runs in both `verify` jobs, so the `lsof` reader is checked on macOS and the `/proc` reader on Linux.

## The app's side

A Debug build launched with `--live-test-loopback` accepts a pairing link whose endpoint is `http://127.0.0.1:<port>`, and on launch forgets any computer it paired through such a link. Without the argument, and in every Release build, a pairing link must be `https` on a `*.ts.net` name. `PairingLinkLoopbackTests` checks both.

The Debug app's `Info.plist` gets `NSAllowsLocalNetworking` from a build phase, so the app can reach the loopback endpoint. `scripts/ios-release-plist-check.sh` fails a Release app that has any App Transport Security key; `ios-release` and `scripts/ios-sign.sh verify` run it.

## Live UI tests

Live tests are in the `live` tier of `native/ui-test-tiers.txt`, which no pull request, push or `ui-all` run includes. They act on hosts only through the control port, and each pairs through the pairing form:

| Test | Checks |
|---|---|
| `LiveHostUITests` pairing | `Live A`'s two ended conversations are listed under All, each once |
| `LiveHostUITests` reply | a reply written on the host shows in the open conversation without a reload |
| `LiveHostUITests` down and back | a stopped host's conversation is marked unavailable; once it starts again the conversation is available and a new reply shows live, without relaunching the app |
| `LiveHostUITests` finished turn | a conversation with an open turn is listed as Working and its header says Working; once the turn finishes the reply shows and the header stops saying Working within 10 seconds. It prints how long that took |
| `LiveHostsUITests` two hosts | each host lists its own conversation; stopping one marks only its conversation unavailable |
| `LiveHostsUITests` file suggestions | typing `@` in the git checkout's conversation lists its files from git on the host, not the ignored one |

To run them on a Mac:

```
go build -o "$T/agenthail" ./cmd/agenthail
go build -o "$T/ios-live-harness" ./cmd/ios-live-harness
"$T/ios-live-harness" -agenthail "$T/agenthail" -hosts 2 -- \
  scripts/ios-simulator.sh iphone run xcodebuild test -project native/Agenthail.xcodeproj -scheme AgenthailIOS \
    -destination "platform=iOS Simulator,name=Agenthail iPhone" -derivedDataPath ~/Library/Developer/Xcode/DerivedData/<name> \
    -only-testing:AgenthailIOSUITests/LiveHostUITests -only-testing:AgenthailIOSUITests/LiveHostsUITests
```

## Nightly run

`iOS live nightly` runs every night on the self-hosted Mac, and on `workflow_dispatch`, from `main` only. It builds both binaries, resets the keychain of the runner's own simulator, `Agenthail CI <runner name>`, and runs the `live` tier under the harness. `scripts/ios-ci-keychain-reset.sh` refuses unless it runs in a self-hosted GitHub Actions job and the simulator is that runner's own, so it never resets a simulator a person uses.

A failed run opens the issue `Nightly iOS live tests are failing`, labelled `nightly-ios-live` and assigned to the repository owner, or comments on it if it is open. The report names the failing test, quotes the harness's lines and links the run and its results; the next passing run closes the issue.
