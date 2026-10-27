# Self-hosted CI runners

Pull requests from branches in this repository run `verify`, `native` and `ios` on self-hosted Mac runners labelled `agenthail`, and `package` and `installer` in disposable macOS virtual machines labelled `agenthail-vm`. Pull requests from forks run all of these jobs on GitHub-hosted runners. `ios-release` always runs on a GitHub-hosted runner: for a branch in this repository it signs, archives and exports the iPhone app and its notification service with the release credentials, without uploading, and that makes a temporary signing keychain the runner's default.

On an ordinary pull request `changes`, `verify` and `verify-linux` run, and `ios-release` runs when iOS inputs change. `native`, `installer`, `package` and `ios` run only on merge-train pull requests (head branch `train/...`) or on a pull request labelled `full-ci`; `ios` still needs changed iOS inputs. Adding the label starts a new run with those jobs. Adding any label starts a new run that replaces the one in progress, so add labels before a long run starts. Otherwise those jobs are skipped, and a skipped required check counts as passed, so on a worker pull request they prove nothing. The merge train is the gate: merge worker pull requests only through a train, never directly.

## Fork protection

The `runs-on` expression in a workflow comes from the pull request, so a fork can change it. Two controls that a pull request cannot change keep fork code off the runners:

- The repository requires approval before workflows run for every outside contributor (`fork-pr-contributor-approval` set to `all_external_contributors`).
- Each runner runs `scripts/ci-runner-job-started.sh` as its job-started hook. It fails any pull request job whose head repository is not this repository, before any step runs, and fails any job whose event it cannot read, so a missing `jq` or a malformed event never lets a job through. The runner reads the hook from a copy outside the repository, so a pull request cannot edit it.

## Runner setup

Each runner is a separate service under the same macOS user, in `~/actions-runner-agenthail-N`, registered with the labels `self-hosted,macOS,ARM64,agenthail`.

1. Install Xcode, select it with `sudo xcode-select -s`, then run `sudo xcodebuild -license accept`, `sudo xcodebuild -runFirstLaunch` and `xcodebuild -downloadPlatform iOS`.
2. Install `jq` and `xcodegen` with Homebrew.
3. Configure the runner with `PATH` including `/opt/homebrew/bin`, then `./svc.sh install && ./svc.sh start`.
4. Copy `scripts/ci-runner-job-started.sh` to `~/actions-runner-agenthail-hooks/job-started.sh` and add `ACTIONS_RUNNER_HOOK_JOB_STARTED=<that path>` to the runner's `.env`. Restart the runner service.

## Job isolation

`scripts/ci-self-hosted.sh prepare` gives `verify` and `native` a scratch `HOME` and `CFFIXED_USER_HOME` inside the job temp directory, and keeps the Go and npm caches per runner. `TMPDIR` and `GOTMPDIR` point to a new directory directly under the user's own temp directory in `/var/folders`, the same place hosted runners and users' Macs use, so tests see that `/var` resolves to `/private/var`. Its name is four characters, because tests create Unix sockets in it and macOS limits a socket path to 104 bytes. A marker file per runner records the directory. If a runner was killed before cleanup, the next `prepare` stops the processes still running from the old directory and deletes it. The iOS jobs keep the real `HOME` because the simulator service uses it. Each runner uses its own simulator, named `Agenthail CI <runner name>`, and its own DerivedData next to its workspace.

`verify` runs `scripts/ci-self-hosted.sh trim-go-cache` after Go is set up. When the runner's Go build cache is over 8 GB, it clears the cache with `go clean -cache`, because a busy runner fills the cache faster than Go's own five-day trim empties it. The script refuses a `RUNNER_TOOL_CACHE` that is relative or contains `.` or `..`, and the trim only acts when the cache path resolves to exactly the runner's tool cache plus `agenthail-cache/go-build`. `scripts/test-ci-self-hosted.sh` checks that guard.

`scripts/ci-self-hosted.sh cleanup` runs after every self-hosted job, including failed and cancelled ones. It stops processes started from the job's workspace or temp directories, erases the job's simulator, resets the workspace, and deletes the scratch `HOME`, the job's temp directory and its marker.

## Disposable VM runners

`package` and `installer` install the package system-wide with `sudo`, register a LaunchAgent and launch the app, so they run in a macOS virtual machine that is created for one job and deleted afterwards. The host is never modified. macOS allows two macOS virtual machines per host, so there are at most two slots.

The VMs use [Tart](https://tart.run) with Softnet networking. Softnet blocks private address ranges except the VM gateway, which is the host, so the supervisor also blocks `192.168.0.0/16`, and the image resolves DNS through public resolvers instead of the gateway. The guest reaches the internet but not the host, the local network or the tailnet.

### Image

`ci/vm/build-image.sh` builds `agenthail-ci-YYYYMMDD` from `ghcr.io/cirruslabs/macos-tahoe-base:latest`: an 80 GB disk, the host's Xcode copied in through a read-only shared folder, sleep and Spotlight off, public DNS resolvers, and the GitHub Actions runner. The image holds no copy of the job-started hook: the runner's `.env` points at `~/actions-runner-hooks/job-started.sh`, and `~/actions-runner-hooks` links to `/Volumes/My Shared Files/hooks`, where the supervisor shares the host runners' hook directory read-only at run time. Updating the host copy updates the VMs on their next job, with no rebuild. The base image's `admin` user logs in automatically and has passwordless `sudo`, which the install tests use. Provisioning runs through `tart exec`, which talks to the guest agent rather than the network.

The script refuses to start with less than 60 GB free. It flushes the guest disk before shutting the VM down, then boots a fresh clone of the new image and checks the runner files, that the guest reads the same job-started hook as the host copy by sha256, Python 3.10 or newer, Xcode and DNS. It publishes the image only if every check passes. After publishing it deletes older `agenthail-ci-*` images and the cached base image. Rebuild it after updating Xcode or the runner version in the script, or monthly to pick up base image updates. Slots pick up the newest image on their next job.

### Supervisor

`ci/vm/supervisor.sh` runs as one LaunchAgent and manages up to `AGENTHAIL_CI_VM_SLOTS` VMs (default 2). It keeps no VM running while nothing is queued:

1. Every 20 seconds it lists queued jobs labelled `agenthail-vm` and starts one worker per queued job that no booting or waiting VM already covers.
2. A worker refuses to clone when less than 25 GB is free, clones the newest image (an APFS clone, so it takes no time or space up front), sets its CPU and memory (`AGENTHAIL_CI_VM_CPU`, `AGENTHAIL_CI_VM_MEMORY_MB`), boots it with the host's hook directory (`AGENTHAIL_CI_HOOKS_DIR`, default `~/actions-runner-agenthail-hooks`) shared read-only, mints a GitHub App installation token on the host and generates a just-in-time runner configuration for one job.
3. Only that configuration and the host hook's sha256 enter the guest. The guest starts the runner only if its `.env` sets the hook and the hook it resolves through the share has that sha256; a missing or different hook exits without starting the runner, and so does a missing hook on the host, before the VM boots. Both count toward the circuit breaker below. The runner exits after one job.
4. The worker then stops and deletes the VM and removes the runner registration, whether the job passed or failed. A VM whose runner gets no job within 5 minutes is discarded the same way.
5. The supervisor kills any VM older than 50 minutes (the longest job timeout plus 5 minutes). On start and on exit it stops every worker and deletes every `agenthail-ci-slot-*` VM, so restarting the LaunchAgent leaves nothing behind. Every 10 minutes it also deletes offline, idle runner registrations older than 50 minutes, which VMs stopped mid-job leave behind once GitHub has finished those jobs. A newer registration may belong to a VM that is still starting its runner.
6. If a slot's runner exits twice in a row without taking a job, the supervisor logs the last runner output, writes the reason to `~/Library/Caches/agenthail-ci-vm/tripped`, deletes its VMs and exits. It will not start again until that file is removed. A worker that fails for any other reason pauses its slot for 5 minutes.

Runner output and the runner's diagnostic log for each slot are kept in `~/Library/Logs/agenthail-ci-vm/`.

The supervisor needs `AGENTHAIL_CI_APP_ID`, the GitHub App's id, and `AGENTHAIL_CI_APP_KEY_COMMAND`, a command that prints the App's private key, for example one that reads it from the login keychain or a secret manager. It runs that command each time it mints a token; the key is never written to disk or passed to the guest.

### Host setup

1. Install Tart and Softnet with Homebrew, and let Softnet run as root without a password: add `<user> ALL=(root) NOPASSWD: <homebrew prefix>/bin/softnet` to a file in `/etc/sudoers.d/` and check it with `sudo visudo -cf <file>`.
2. Clone this repository on the host and run `ci/vm/build-image.sh` from it.
3. Create a LaunchAgent that runs `/bin/bash <clone>/ci/vm/supervisor.sh` with `RunAtLoad`, `KeepAlive` set to restart only after an unsuccessful exit (`SuccessfulExit` false), an `ExitTimeOut` of 60 seconds, a log file, and `EnvironmentVariables` holding `AGENTHAIL_CI_APP_ID` and `AGENTHAIL_CI_APP_KEY_COMMAND`, then load it with `launchctl bootstrap gui/$(id -u) <plist>`.
4. Turn off system sleep on the host: `sudo pmset -a sleep 0 disksleep 0 powernap 0`.

## Stacked pull requests

A GitHub stack is a chain of dependent pull requests; position 1 is the bottom and the top contains every change in the stack. Pull requests below the top skip `verify`, `native`, `installer`, `package`, `ios` and `ios-release`. The top runs them all. A skipped job reports as skipped, which branch protection accepts, so the required checks keep their names. `changes` and `verify-linux` run on every pull request. Pull requests outside a stack are unaffected.

For a stacked pull request, the `changes` gate diffs against the merge base with the stack's base branch, not the pull request below. The top therefore runs `ios` when any pull request in the stack changes an iOS input. If the event has no stack base branch, the gate runs `ios`.

Because only the top runs the expensive jobs, three rules apply:

1. Merge a whole stack at once. GitHub lets a lower pull request merge on its own once the pull requests below it pass, but their expensive jobs were skipped, so that code would reach `main` without them.
2. After removing or closing the top pull request, push a commit to the new top before merging (`git commit --allow-empty` is enough). Its last run was below the top and skipped the expensive jobs. Re-running that run does not help: a re-run replays the original event, including the old stack position.
3. Stacks are a GitHub preview. If the `stack` field disappears from the event, every pull request runs every job.
