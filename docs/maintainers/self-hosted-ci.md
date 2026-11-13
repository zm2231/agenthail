# Self-hosted CI runners

Pull requests from branches in this repository run `verify` and `ios` on self-hosted Mac runners labelled `agenthail`, and `package` and `installer` in disposable macOS virtual machines labelled `agenthail-vm`. Pull requests from forks run all of these jobs on GitHub-hosted runners. `ios-release` always runs on a GitHub-hosted runner: for a branch in this repository it signs, archives and exports the iPhone app and its notification service with the release credentials, without uploading, and that makes a temporary signing keychain the runner's default.

A push to `main` runs the full jobs on these runners, with `ios` when iOS inputs changed since the previous push and without `ios-release`. A later push cancels the earlier run, so the newest commit is what gets a result. Pull requests don't have to be up to date with `main` to merge, so this run is what tests changes together; check it before tagging a release.

On an ordinary pull request `changes`, `verify` and `verify-linux` run, and `ios` and `ios-release` run when iOS inputs change. `ios` runs the UI test tiers described under [iOS UI test tiers](#ios-ui-test-tiers). `installer`, `package` and the native helper and simulator guard steps of `verify` run only on merge-train pull requests (head branch `train/...`) or on a pull request labelled `full-ci`. Otherwise they are skipped, and a skipped required check counts as passed, so on a worker pull request they prove nothing. The native steps are part of `verify` so they don't wait for a Mac runner of their own; `verify` waits for `gate`, and runs them too if `gate` failed to decide. The merge train is the gate: merge worker pull requests only through a train, never directly.

`verify-linux` runs on GitHub-hosted runners as six jobs. `verify-linux-rest` runs vet and the race tests of every package except `internal/daemon` and `internal/surface/surfaces`. `verify-linux-surfaces` runs the Node host tests and `internal/surface/surfaces`'s race tests, the slowest package after the daemon. The four `verify-linux-daemon` jobs each run a quarter of `internal/daemon`'s race tests by time. `scripts/go-test-shard` reads the package's top-level tests from its source, as `go test` selects them, with the same build flags as the run. It deals them longest first to the shard with the least time so far, using the seconds `scripts/go-test-shard/seconds/internal-daemon.tsv` records for each test; a test the file lacks counts as its median. A new test therefore still lands in exactly one shard, and nothing in the package runs to list them. When the shards drift apart, record the file again from a race run: `go test ./internal/daemon -race -gcflags='modernc.org/...=-d=checkptr=0 -race=false' -count=1 -timeout 25m -json | go run ./scripts/go-test-shard -record github.com/zm2231/agenthail/internal/daemon > scripts/go-test-shard/seconds/internal-daemon.tsv`. The first shard then compares `go test -race -list` with the union of the shards and fails on any difference, so a test the tool cannot see fails the job rather than silently not running. The `verify-linux` job itself only runs `scripts/ci-needs-passed.sh`, which passes only if all six jobs succeeded: a skipped or cancelled job fails it. The required check therefore keeps its name.

macOS `verify` runs `go test` without `-race`, and only on the packages `scripts/go-darwin-packages` lists: those whose macOS build, tests included, has files their Linux build lacks, and those that read `runtime.GOOS`. The list comes from the source on every run, so a new macOS file or `runtime.GOOS` check adds its package without an edit here. `scripts/ci-darwin-tests.sh` runs those packages, `internal/daemon` as three shards of one compiled test binary, the dashboard readability guard and, on full runs, the native helper tests all at once through `scripts/ci-concurrently.sh`, which prints each one's output whole once all have finished. Before the shards start, it compares the binary's `-test.list` with the union of the shards, as the Linux shards do. The Node host tests run only on Linux: `--omit=optional` leaves out their platform builds, so a macOS run adds nothing.

Labels don't start runs. CI and iOS run when a pull request is opened, reopened or pushed to. Their `gate` and `changes` jobs read the pull request's labels through the API when they run, rather than from the event. `gh pr create --label` opens the pull request before it adds the labels, so the labels the event carries would be missing. Adding `full-ci` later runs `Full CI label` (`scripts/ci-full-label.sh`):

- If the latest run's gate is still deciding, it picks the label up by itself.
- If the gate already skipped the full jobs, the run is rerun, cancelled first if it is still going.
- If the run already has the full jobs, it is left alone. So is a cancelled run, such as one a merge train cancelled.

Adding `ui-all` reruns the latest iOS run, cancelled first if it is still going, so its `changes` job reads the label and `ios` runs every UI test tier. An iOS run with no iOS changes is left alone.

Any other label changes nothing. Adding `full-ci` to a pull request from a fork reruns nothing, because its token cannot rerun workflows; rerun CI by hand after adding the label. `scripts/test-ci-full-label.sh` checks these decisions against a stub `gh`.

## iOS UI test tiers

`native/ui-test-tiers.txt` lists every iPhone UI test once, with the tier that runs it. `ios` always runs every unit test and adds UI tests by tier:

| Run | UI test tiers |
|---|---|
| Pull request, including `full-ci` and `train/...` | `smoke` |
| Pull request that changes the transcript, the preview daemon or the UI test harness | `smoke`, `main` |
| Pull request labelled `ui-all` | `smoke`, `main`, `nightly` |
| Push to `main` | `smoke`, `main` |
| `iOS nightly`, every night and on `workflow_dispatch`, on iPhone and on iPad | `smoke`, `main`, `nightly` |

Tests in the `live` tier pair the app with real daemons, so they run only under `cmd/ios-live-harness` in `iOS live nightly`, never in the runs above. See [iPhone live harness](ios-live-harness.md).

`scripts/ios-ui-tests.sh tiers` decides the tiers from the event, the changed files and the labels; the paths that widen a pull request to `main` are in its `wide` pattern. `scripts/ios-ui-tests.sh check` fails when a UI test is in no tier, in two, or a listed test no longer exists, so a new test cannot silently never run. `ios`, `iOS nightly` and `verify` run the check, and `scripts/test-ios-ui-tests.sh` checks the script.

A failed `ios` run on a push to `main` opens the issue `iOS tests on main are failing`, assigned to the repository owner, or comments on it if it is open; the next passing push closes it. A cancelled run, such as one a later push replaced, reports nothing. `iOS nightly` runs the suite once on an iPhone simulator and once on an 11-inch iPad Pro simulator, through `.github/workflows/ios-device-tests.yml`. Each device reports on its own issue, `Nightly iOS tests are failing` and `Nightly iPad tests are failing`, and a run that stopped or was cancelled is reported too. Pull requests and pushes run the iOS tests on iPhone only. Both use `scripts/ci-failure-issue.sh`, which `scripts/test-ci-failure-issue.sh` checks.

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
5. Turn off crash reporter dialogs for the runner's user: `defaults write com.apple.CrashReporter DialogType none`. See [Crash reporter dialogs](#crash-reporter-dialogs).

## Crash reporter dialogs

When an app crashes, macOS can show a crash dialog, and the crash reporter waits for it to be closed. Nobody closes it on a runner. At the end of every UI test the test runner waits for the crash reporter, about 300 seconds each time, so a test that takes seconds takes five minutes and the `ios` job runs into its timeout. Setting the user's `com.apple.CrashReporter` `DialogType` to `none` turns the dialogs off.

The self-hosted `ios` job checks this setting first and fails with a message naming the fix unless it reads `none`. The test step runs through `scripts/ios-test-stall-guard.sh`, which stops the tests and names this cause once three tests have each taken more than 200 seconds. `scripts/test-ios-test-stall-guard.sh` checks the guard against a stub test command.

## Job isolation

`scripts/ci-self-hosted.sh prepare` gives `verify` a scratch `HOME` and `CFFIXED_USER_HOME` inside the job temp directory, and keeps the Go and npm caches per runner. `TMPDIR` and `GOTMPDIR` point to a new directory directly under the user's own temp directory in `/var/folders`, the same place hosted runners and users' Macs use, so tests see that `/var` resolves to `/private/var`. Its name is four characters, because tests create Unix sockets in it and macOS limits a socket path to 104 bytes. A marker file per runner records the directory. If a runner was killed before cleanup, the next `prepare` stops the processes still running from the old directory and deletes it. The iOS jobs keep the real `HOME` because the simulator service uses it. Each runner uses its own simulator, named `Agenthail CI <runner name>`, and its own DerivedData next to its workspace. The `ios` test run passes `-collect-test-diagnostics never`: after the tests pass, collecting simulator diagnostics can wait 600 seconds and time out, which adds ten minutes to the job.

`verify` runs `scripts/ci-self-hosted.sh trim-go-cache` after Go is set up. When the runner's Go build cache is over 3 GB, it clears the cache with `go clean -cache`, because a busy runner fills the cache faster than Go's own five-day trim empties it. A cold `verify` run writes about 1.1 GB to the cache, so 3 GB keeps a few branches' builds warm, and the three runners' caches stay under about 12 GB together, counting the job that runs after the trim. The script refuses a `RUNNER_TOOL_CACHE` that is relative or contains `.` or `..`, and the trim only acts when the cache path resolves to exactly the runner's tool cache plus `agenthail-cache/go-build`. `scripts/test-ci-self-hosted.sh` checks that guard.

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
2. A worker refuses to clone when less than 15 GB is free, clones the newest image (an APFS clone, so it takes no time or space up front), sets its CPU and memory (`AGENTHAIL_CI_VM_CPU`, `AGENTHAIL_CI_VM_MEMORY_MB`), boots it with the host's hook directory (`AGENTHAIL_CI_HOOKS_DIR`, default `~/actions-runner-agenthail-hooks`) shared read-only, mints a GitHub App installation token on the host and generates a just-in-time runner configuration for one job.
3. Only that configuration and the host hook's sha256 enter the guest. The guest starts the runner only if its `.env` sets the hook and the hook it resolves through the share has that sha256; a missing or different hook exits without starting the runner, and so does a missing hook on the host, before the VM boots. Both count toward the circuit breaker below. The runner exits after one job.
4. The worker then stops and deletes the VM and removes the runner registration, whether the job passed or failed. A VM whose runner gets no job within 5 minutes is discarded the same way.
5. The supervisor kills any VM older than 50 minutes (the longest job timeout plus 5 minutes). On start and on exit it stops every worker and deletes every `agenthail-ci-slot-*` VM, so restarting the LaunchAgent leaves nothing behind. Every 10 minutes it also deletes offline, idle runner registrations older than 50 minutes, which VMs stopped mid-job leave behind once GitHub has finished those jobs. A newer registration may belong to a VM that is still starting its runner.
6. If a slot's runner exits twice in a row without taking a job, the supervisor logs the last runner output, writes the reason to `~/Library/Caches/agenthail-ci-vm/tripped`, deletes its VMs and exits. It will not start again until that file is removed. A worker that fails for any other reason pauses its slot for 5 minutes.

Runner output and the runner's diagnostic log for each slot are kept in `~/Library/Logs/agenthail-ci-vm/`.

The supervisor needs `AGENTHAIL_CI_APP_ID`, the GitHub App's id, and `AGENTHAIL_CI_APP_KEY_COMMAND`, a command that prints the App's private key, for example one that reads it from the login keychain or a secret manager. It runs that command each time it mints a token; the key is never written to disk or passed to the guest.

### Disk space

A clone takes no space up front, so the 15 GB floor is room for what may still be written after the clone starts: this VM's growth, the other VM's, and the jobs on the host runners. A running VM has been seen to use about 3 GB of the host's disk, and a cold Go build cache about 1.1 GB. When a worker finishes, the supervisor logs the free space at its clone and the lowest free space while the VM ran. If that lowest value falls under about 5 GB, raise the floor.

While queued jobs are refused for space, the supervisor opens one issue, `macOS VM runners are out of disk space`, and adds a comment at most every 30 minutes with the free space. The time of the last report is kept in `~/Library/Caches/agenthail-ci-vm/disk-issue-noted`, so a restart does not report again early. Its token asks for `issues: write`, so the GitHub App needs that permission. The issue number is kept in `~/Library/Caches/agenthail-ci-vm/disk-issue`. When a VM next starts, the supervisor comments with the free space and closes the issue.

With no job running and the runners' Go build caches at their 3 GB limit, the host should have at least 25 GB free. That covers the 15 GB floor, about 3 GB for the first VM before the second clones, and about 2 GB of workspace and temp files for each of the three host runners' jobs. The second VM's growth comes after its clone, so it comes out of the floor. Other VMs and files on the host count against this figure like anything else on the disk.

### Updating the supervisor

The LaunchAgent runs its own copy of `supervisor.sh`, the second entry of its `ProgramArguments`, so a change merged here takes effect only after that copy is replaced and the LaunchAgent restarted. Restart it only while no slot is running a job. On the host, from an up-to-date clone of this repository:

```sh
plist="$(grep -l supervisor.sh ~/Library/LaunchAgents/*.plist)"
label="$(/usr/libexec/PlistBuddy -c 'Print :Label' "$plist")"
copy="$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:1' "$plist")"
if find ~/Library/Caches/agenthail-ci-vm -name 'slot-*.phase' -exec grep -lx busy {} + | grep -q .; then
  echo "a slot is running a job; try again when it finishes"
else
  cp ci/vm/supervisor.sh "$copy" && launchctl kickstart -k "gui/$(id -u)/$label"
fi
```

`kickstart -k` stops the running supervisor, which stops its workers and deletes every `agenthail-ci-slot-*` VM, then starts the new copy. Jobs still queued on GitHub stay queued; the new supervisor starts VMs for them on its first poll, as soon as it starts. A VM that was booting or waiting for a job is deleted, and the job it would have taken stays queued for the next VM. A VM running a job is deleted too, and GitHub fails that job when its runner disappears, which is why the commands check for a busy slot first. A slot can still take a job in the seconds between the check and the restart; if one does, re-run that job.

### Host setup

1. Install Tart and Softnet with Homebrew, and let Softnet run as root without a password: add `<user> ALL=(root) NOPASSWD: <homebrew prefix>/bin/softnet` to a file in `/etc/sudoers.d/` and check it with `sudo visudo -cf <file>`.
2. Clone this repository on the host and run `ci/vm/build-image.sh` from it.
3. Create a LaunchAgent that runs `/bin/bash <clone>/ci/vm/supervisor.sh` with `RunAtLoad`, `KeepAlive` set to restart only after an unsuccessful exit (`SuccessfulExit` false), an `ExitTimeOut` of 60 seconds, a log file, and `EnvironmentVariables` holding `AGENTHAIL_CI_APP_ID` and `AGENTHAIL_CI_APP_KEY_COMMAND`, then load it with `launchctl bootstrap gui/$(id -u) <plist>`.
4. Turn off system sleep on the host: `sudo pmset -a sleep 0 disksleep 0 powernap 0`.

## Stacked pull requests

A GitHub stack is a chain of dependent pull requests; position 1 is the bottom and the top contains every change in the stack. Pull requests below the top skip `verify`, `installer`, `package`, `ios` and `ios-release`. The top runs them all. A skipped job reports as skipped, which branch protection accepts, so the required checks keep their names. `changes` and `verify-linux` run on every pull request. Pull requests outside a stack are unaffected.

For a stacked pull request, the `changes` gate diffs against the merge base with the stack's base branch, not the pull request below. The top therefore runs `ios` when any pull request in the stack changes an iOS input. If the event has no stack base branch, the gate runs `ios`.

Because only the top runs the expensive jobs, three rules apply:

1. Merge a whole stack at once. GitHub lets a lower pull request merge on its own once the pull requests below it pass, but their expensive jobs were skipped, so that code would reach `main` without them.
2. After removing or closing the top pull request, push a commit to the new top before merging (`git commit --allow-empty` is enough). Its last run was below the top and skipped the expensive jobs. Re-running that run does not help: a re-run replays the original event, including the old stack position.
3. Stacks are a GitHub preview. If the `stack` field disappears from the event, every pull request runs every job.
