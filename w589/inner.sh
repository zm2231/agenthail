#!/bin/bash
# comment-checker-disable-file
# Runs inside ios-live-harness: drives the live turn, runs the test app against host 0, and has it capture its own window.
set -uo pipefail
kit="$1"; dir="$2"; bundle="$3"; out="$dir/out"; frames="$out/frames"
exe="$dir/Agenthail.app/Contents/MacOS/AgenthailW589"
mkdir -p "$frames" "$dir/apphome" "$dir/apptmp"
python3 -I "$kit/drive.py" "$dir/agenthail" "$dir/Agenthail.app" "$out" "$bundle" &
drive=$!
stop() { for pid in "$@"; do [ -n "$pid" ] && kill -TERM "$pid" 2>/dev/null; done; }
for _ in $(seq 1 600); do [ -f "$out/fixture.ready" ] && break; sleep 0.5; done
[ -f "$out/fixture.ready" ] || { echo "fixture never got ready; see $out/drive.log"; stop "$drive"; exit 1; }
session=$(sed -n 1p "$out/session.txt"); endpoint=$(sed -n 2p "$out/session.txt")
token=$(cat "$W589_BASE"/h0.*/home/.agenthail/dashboard.token)
env -i HOME="$dir/apphome" CFFIXED_USER_HOME="$dir/apphome" PATH=/usr/bin:/bin TMPDIR="$dir/apptmp/" \
  AGENTHAIL_API_URL="$endpoint" AGENTHAIL_API_TOKEN="$token" \
  DYLD_INSERT_LIBRARIES="$dir/w589capture.dylib" W589_FRAMES="$frames" \
  "$exe" -lastSelectedSessionID "$session" >"$out/app.log" 2>&1 &
app=$!
echo "app pid $app session $session bundle $bundle" >>"$out/drive.log"
sleep 12
if ! kill -0 "$app" 2>/dev/null; then echo "the test app exited early; see $out/app.log"; stop "$drive"; exit 1; fi
touch "$frames/go" "$out/capture.started"
wait "$drive"
sleep 3
touch "$frames/stop"
sleep 1
stop "$app"
for _ in $(seq 1 20); do kill -0 "$app" 2>/dev/null || break; sleep 0.5; done
kill -0 "$app" 2>/dev/null && kill -KILL "$app" 2>/dev/null
count=$(ls "$frames"/*.jpg 2>/dev/null | wc -l | tr -d ' ')
if [ "$count" -gt 0 ]; then "$dir/overlap" "$frames"/*.jpg >"$out/suspects.txt"; else : >"$out/suspects.txt"; fi
echo "frames $count, suspect frames $(wc -l <"$out/suspects.txt" | tr -d ' ')" | tee -a "$out/drive.log"
