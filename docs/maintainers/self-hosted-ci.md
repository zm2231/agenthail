# Self-hosted CI runners

Pull requests from branches in this repository run `verify`, `native`, `ios` and `ios-release` on self-hosted Mac runners labelled `agenthail`. Pull requests from forks run the same jobs on GitHub-hosted runners. `package` and `installer` always run on GitHub-hosted runners because the install test installs the package system-wide with `sudo`.

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
