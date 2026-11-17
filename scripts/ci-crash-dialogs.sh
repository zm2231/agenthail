#!/usr/bin/env bash
# The read is retried: on a loaded host it has failed while the setting was right.
set -u

attempts="${CRASH_DIALOG_ATTEMPTS:-3}"
pause="${CRASH_DIALOG_PAUSE:-2}"
read_value() { defaults read com.apple.CrashReporter DialogType 2>&1; }

for attempt in $(seq 1 "$attempts"); do
  value="$(read_value)"
  status=$?
  if [ "$status" -eq 0 ] && [ "$value" = none ]; then
    exit 0
  fi
  if [ "$attempt" -lt "$attempts" ]; then
    sleep "$pause"
  fi
done

echo "Read com.apple.CrashReporter DialogType $attempts times; the last read exited $status with: ${value:-<nothing>}"
echo "::error::Crash reporter dialogs are not turned off for this runner's user, so every UI test teardown can wait about 300 s on one. Run 'defaults write com.apple.CrashReporter DialogType none' as that user. See Crash reporter dialogs in docs/maintainers/self-hosted-ci.md."
exit 1
