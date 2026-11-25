#!/bin/sh
set -eu
bin=${BIN:-/Volumes/4/caches/agenthail-v2-target/release/agenthail-views}
out=${1:-/Volumes/4/tmp/agenthail-collab/end-state}
mkdir -p "$out"
snap() { name=$1; shift; "$bin" --snap "$out/$name.png" --size 1280x800 --scale 2 --theme dark "$@" >/dev/null; }
snap 01-now-panes --view now
snap 01b-now-collapsed --view now-collapsed
snap 01c-now-focused --view now-focused
snap 02-now-brief --view now-brief
snap 03-session --view session --session cd55560f-9682-4c82-b2da-7731e3d47dbb
snap 04-project-chat --view project-chat --project repo:github.com/zm2231/agenthail
snap 04b-project-chat-crew --view project-chat --project repo:github.com/storyboard-fs/contract-parser
snap 05-project-board --view project-board --project repo:github.com/zm2231/agenthail
snap 06-work --view work --item agenthail-W595
snap 07-jump --view jump
snap 08-session-ask --view session --session c1fafaa9-b321-4c5e-8895-dd8ad83cd9a3
snap 09-sessions --view sessions
snap 10-sessions-working --view sessions-working
ls "$out"
