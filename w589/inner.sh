#!/bin/bash
# comment-checker-disable-file
# Runs inside ios-live-harness: drives the live turn, runs the app against host 0, captures its window.
set -uo pipefail
kit="$1"; dir="$2"; out="$dir/out"; bundle="com.agenthail.app.w589"
mkdir -p "$out/frames"
python3 -I "$kit/drive.py" "$dir/agenthail" "$dir/Agenthail.app" "$out" "$bundle" &
drive=$!
stop() { for pid in "$@"; do [ -n "$pid" ] && kill -TERM "$pid" 2>/dev/null; done; }
for _ in $(seq 1 600); do [ -f "$out/fixture.ready" ] && break; sleep 0.5; done
[ -f "$out/fixture.ready" ] || { echo "fixture never got ready; see $out/drive.log"; stop "$drive"; exit 1; }
session=$(sed -n 1p "$out/session.txt"); endpoint=$(sed -n 2p "$out/session.txt")
token=$(cat "$W589_BASE"/h0.*/home/.agenthail/dashboard.token)
mkdir -p "$dir/apphome"
env -i HOME="$dir/apphome" CFFIXED_USER_HOME="$dir/apphome" PATH=/usr/bin:/bin TMPDIR="$dir/apptmp/" \
  AGENTHAIL_API_URL="$endpoint" AGENTHAIL_API_TOKEN="$token" \
  "$dir/Agenthail.app/Contents/MacOS/Agenthail" -lastSelectedSessionID "$session" >"$out/app.log" 2>&1 &
app=$!
echo "app pid $app session $session" >>"$out/drive.log"
wid=""
for _ in $(seq 1 60); do wid=$("$dir/windowid" "$app" 2>/dev/null) && [ -n "$wid" ] && break; sleep 0.5; done
[ -n "$wid" ] || { echo "no app window found"; stop "$app" "$drive"; exit 1; }
sleep 6
touch "$out/capture.started"
i=0
tail=""
while true; do
  screencapture -l "$wid" -x -o -t jpg "$out/frames/$(printf %05d "$i").jpg" 2>>"$out/capture.err" || true
  i=$((i + 1))
  [ -f "$out/drive.done" ] && [ -z "$tail" ] && tail=$i
  [ -n "$tail" ] && [ $((i - tail)) -ge 30 ] && break
  sleep 0.1
done
stop "$app"
wait "$drive"
"$dir/overlap" "$out"/frames/*.jpg >"$out/suspects.txt"
echo "frames $i, suspect frames $(wc -l <"$out/suspects.txt")" | tee -a "$out/drive.log"
