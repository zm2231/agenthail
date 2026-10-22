# Linux daemon

Agenthail runs on Linux as a headless daemon for Claude Code and Codex. The daemon is the same pure Go binary as on macOS; what differs per operating system is who supervises it.

## One service owner per operating system

`internal/cli/service.go` defines `daemonService`. `App.service()` picks one implementation from `runtime.GOOS` the first time it is needed, and every lifecycle path (`daemon start|stop|restart|status|install|uninstall`, dashboard enable and remote setup, `doctor`) goes through it. Tests inject a fake.

| OS | Implementation | Supervisor |
|---|---|---|
| darwin | `launchdService` (`service_launchd.go`) | `~/Library/LaunchAgents/com.agenthail.daemon.plist`, or Homebrew's `homebrew.mxcl.agenthail` |
| linux | `systemdService` (`service_systemd.go`) | `~/.config/systemd/user/agenthail.service` |
| other | `unsupportedService` | none; `start` and `stop` manage an unsupervised daemon |

Without an installed service, `start`, `stop` and `restart` manage an unsupervised daemon on every OS.

## The systemd unit

`daemon install` writes:

- `ExecStart=<resolved agenthail path> daemon-run`
- `Environment=` lines for `PATH`, `CODEX_HOME`, `CLAUDE_CONFIG_DIR` and the `AGENTHAIL_*` overrides taken from the installing shell, plus `AGENTHAIL_DAEMON_SUPERVISOR=systemd`
- `Restart=always`, `RestartSec=2` and `StartLimitIntervalSec=0`, so a crashing daemon keeps being restarted instead of leaving the unit failed
- `KillMode=process`
- `StandardOutput=` and `StandardError=` appending to `~/.agenthail/daemon.log`, which install rotates at 1 MiB

`KillMode=process` matters because tmux servers and Codex app-servers that the daemon launches start inside the unit's cgroup. With the default `control-group` mode, every `systemctl --user stop` or `restart` would kill those agents. With `process`, systemd signals only the daemon. The daemon still stops the Claude peer workers it owns when it shuts down.

Install then runs `daemon-reload`, `enable` and `restart`, waits for the daemon's pid file, and enables lingering with `loginctl enable-linger <user>`. Without lingering, systemd stops the user manager, and the daemon with it, when the last login session ends. Many distributions refuse self-service lingering. In that case install still succeeds but prints `linger: off` with the `sudo loginctl enable-linger <user>` command, and `doctor` stays unhealthy until lingering is on.

Before writing anything, install refuses a `CODEX_HOME` or `~/.agenthail` that cannot run a program or hold a SQLite database. Codex's app-server daemon installs and runs its own binary under `CODEX_HOME/packages`, and a noexec or virtiofs mount fails that with `EFAULT`.

`AGENTHAIL_DAEMON_SUPERVISOR=systemd` tells the Codex runtime status that the daemon is supervised across reboot, as `XPC_SERVICE_NAME` does under launchd.

## Doctor

`doctor --json` adds `daemon: {supervisor, problems}` and `claudeConfigDirs`, every Claude config root Agenthail reads with the primary one first (`claudeConfigError` when `~/.agenthail/config.json` cannot be read), and `claudeConfigRoots`, each root with the login it currently holds. Under systemd each problem carries `problem`, `detail` and `fix`:

| problem | fix |
|---|---|
| `systemd-missing` | run the daemon unsupervised with `agenthail daemon start` |
| `systemd-user-unreachable` | `loginctl enable-linger <user>`, then reconnect so `XDG_RUNTIME_DIR=/run/user/<uid>` is set |
| `linger-off` | `loginctl enable-linger <user>`, or `sudo loginctl enable-linger <user>` when refused |
| `service-not-installed` | `agenthail daemon install` |
| `service-inactive` | `systemctl --user restart agenthail.service` and inspect the log |
| `claude-missing` | install Claude Code or set `AGENTHAIL_CLAUDE_BIN`, then reinstall the service |
| `state-dir-noexec`, `state-dir-sqlite` | move `~/.agenthail` onto a local exec-capable filesystem |
| `codex-home-noexec`, `codex-home-sqlite` | point `CODEX_HOME` at an exec-capable path in `agenthail.service.d` or rerun `daemon install` with it set |
| `codex-home-drift`, `claude-config-dir-drift` | rerun `daemon install` from a shell with the intended directory |

The Claude, exec and SQLite checks read the environment recorded in the unit, not the calling shell, because that is the environment the daemon runs with. Codex binary and app-server problems come from the Codex surface's runtime status.

## Claude Code on Linux

Agenthail resolves Claude Code state through `surface.ClaudeConfigRoots`: the primary root (`CLAUDE_CONFIG_DIR` when set, otherwise `~/.claude`) plus any `claudeConfigDirs` in `~/.agenthail/config.json` (see [Claude peer messaging](claude-peers.md#claude-config-roots)). Claude Code 2.1.302 keeps `sessions/`, peer key files, `jobs/` and `projects/` there. On Linux it puts peer sockets under `$XDG_RUNTIME_DIR/cc-socks` rather than `/tmp/cc-socks`, and records `procStart` as the starttime field of `/proc/<pid>/stat` in clock ticks rather than as a date. Peer discovery accepts both socket directories and verifies the start time from `/proc`, so a reused PID is not trusted.

Claude Remote Control and Notion depend on the Python sidecar and a signed-in desktop browser. The Linux archive does not ship the sidecar, so off macOS `cmd/agenthail` registers the Notion surface only when `AGENTHAIL_SIDECAR` is set and limits the Claude surface to local sessions, whose health does not probe claude.ai. `list` and `doctor` stay clean on a plain Linux install, including when no Claude session is running.

## Releases and updates

`scripts/package-release.sh` with `GOOS=linux` builds a static binary (`CGO_ENABLED=0`) and an archive without macOS extended attributes, stamped in UTC. The release workflow's `linux-archive` job builds amd64 and arm64.

`agenthail update` treats a Linux build with a release version as an archive install. It:

1. refuses to run unless the install directory holds only archive entries (`agenthail`, `claude-host`, `skills`, `README.md`, `LICENSE`, `COMMERCIAL.md`)
2. downloads the arch archive and checksum, verifies the checksum, and extracts only regular files and directories under the archive root (release packaging refuses any other entry type, so npm bin links are not staged)
3. checks that the new binary's `version --json` reports the release tag
4. renames the install directory to `<dir>.previous` and moves the new one into place
5. when a daemon is running or a service is installed, runs `<dir>/agenthail daemon restart` with the new binary and waits up to 20 seconds for `/api/v1/version` to answer (or, with the dashboard disabled, for the daemon to stay up for 2 seconds)
6. on success deletes `<dir>.previous`; on failure moves the new directory aside, restores the previous one, restarts it, and reports the failure

## Verifying on a Linux host

```bash
GOOS=linux GOARCH=amd64 AGENTHAIL_ALLOW_DIRTY=1 scripts/package-release.sh
```

Copy the archive to the host, unpack it into `~/.local/share/agenthail`, run `agenthail daemon install` and `agenthail doctor`, then:

- start agents with `agenthail thread create claude|codex` and through the dashboard `session-create` action with `launcher: tmux`, and confirm `agenthail list` shows them
- record the agent pids and start times, run `agenthail daemon restart`, and compare
- close every ssh session, wait a minute, reconnect, and confirm `systemctl --user show agenthail -p MainPID -p ExecMainStartTimestamp` is unchanged
- to exercise `agenthail update` against a local server, build with `AGENTHAIL_UPDATE_RELEASE_URL=http://127.0.0.1:<port>/latest`
