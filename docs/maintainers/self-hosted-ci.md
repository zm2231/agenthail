# Self-hosted CI runners

Pull requests from branches in this repository run `verify`, `native`, `ios` and `ios-release` on self-hosted Mac runners labelled `agenthail`, and `package` and `installer` in disposable macOS virtual machines labelled `agenthail-vm`. Pull requests from forks run all of these jobs on GitHub-hosted runners.

## Fork protection

The `runs-on` expression in a workflow comes from the pull request, so a fork can change it. Two controls that a pull request cannot change keep fork code off the runners:

- The repository requires approval before workflows run for every outside contributor (`fork-pr-contributor-approval` set to `all_external_contributors`).
- Each runner runs `scripts/ci-runner-job-started.sh` as its job-started hook. It fails any pull request job whose head repository is not this repository, before any step runs. The runner reads the hook from a copy outside the repository, so a pull request cannot edit it.

## Runner setup

Each runner is a separate service under the same macOS user, in `~/actions-runner-agenthail-N`, registered with the labels `self-hosted,macOS,ARM64,agenthail`.

1. Install Xcode, select it with `sudo xcode-select -s`, then run `sudo xcodebuild -license accept`, `sudo xcodebuild -runFirstLaunch` and `xcodebuild -downloadPlatform iOS`.
2. Install `jq` and `xcodegen` with Homebrew.
3. Configure the runner with `PATH` including `/opt/homebrew/bin`, then `./svc.sh install && ./svc.sh start`.
4. Copy `scripts/ci-runner-job-started.sh` to `~/actions-runner-agenthail-hooks/job-started.sh` and add `ACTIONS_RUNNER_HOOK_JOB_STARTED=<that path>` to the runner's `.env`. Restart the runner service.

## Job isolation

`scripts/ci-self-hosted.sh prepare` gives `verify` and `native` a scratch `HOME`, `CFFIXED_USER_HOME` and `TMPDIR` inside the job temp directory, and keeps the Go and npm caches per runner. The iOS jobs keep the real `HOME` because the simulator service uses it. Each runner uses its own simulator, named `Agenthail CI <runner name>`, and its own DerivedData next to its workspace.

`scripts/ci-self-hosted.sh cleanup` runs after every self-hosted job, including failed and cancelled ones. It stops processes started from the job's workspace or temp directory, erases the job's simulator, and resets the workspace.

## Disposable VM runners

`package` and `installer` install the package system-wide with `sudo`, register a LaunchAgent and launch the app, so they run in a macOS virtual machine that is created for one job and deleted afterwards. The host is never modified. macOS allows two macOS virtual machines per host, so there are at most two slots.

The VMs use [Tart](https://tart.run). The guest reaches the internet but not the host, the local network or the tailnet (Tart's Softnet networking).

### Image

`ci/vm/build-image.sh` builds `agenthail-ci-YYYYMMDD` from `ghcr.io/cirruslabs/macos-tahoe-base:latest`: an 80 GB disk, the host's Xcode copied in through a read-only shared folder, sleep and Spotlight off, and the GitHub Actions runner with the same job-started hook as the host runners. The base image's `admin` user logs in automatically and has passwordless `sudo`, which the install tests use. Provisioning runs through `tart exec`, which talks to the guest agent rather than the network.

The script refuses to start with less than 60 GB free. After a successful build it deletes older `agenthail-ci-*` images and the cached base image. Rebuild it after updating Xcode or the runner version in the script, or monthly to pick up base image updates. Slots pick up the newest image on their next job.

### Slots

`ci/vm/supervisor.sh <slot>` runs one slot in a loop. For each job it:

1. refuses to clone when less than 25 GB is free,
2. clones the newest image (an APFS clone, so it takes no time or space up front) and boots it,
3. mints a GitHub App installation token on the host, removes the slot's offline runners, and generates a just-in-time runner configuration for one job,
4. passes only that configuration into the guest and starts the runner, which exits after one job,
5. stops and deletes the VM. A job that runs longer than 90 minutes is discarded.

The App private key is read from setec over the tailnet each time and never written to disk or passed to the guest.

### Host setup

1. Install Tart and Softnet with Homebrew, and let Softnet run as root without a password: `echo "<user> ALL=(root) NOPASSWD: /opt/homebrew/bin/softnet" | sudo tee /etc/sudoers.d/softnet` (check it with `sudo visudo -cf /etc/sudoers.d/softnet`).
2. Clone this repository to `~/agenthail-ci-vm` and run `ci/vm/build-image.sh`.
3. For each slot N, create `~/Library/LaunchAgents/agenthail.ci-vm-slot-N.plist` that runs `/bin/bash ~/agenthail-ci-vm/ci/vm/supervisor.sh N` with `RunAtLoad` and `KeepAlive`, logging to `~/Library/Logs/agenthail-ci-vm-slot-N.log`, then `launchctl bootstrap gui/$(id -u) <plist>`.
4. Turn off system sleep on the host: `sudo pmset -a sleep 0 disksleep 0 powernap 0`.
