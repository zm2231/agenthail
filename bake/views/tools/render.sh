#!/bin/sh
set -eu
bin=${BIN:-/Volumes/4/caches/agenthail-v2-target/release/agenthail-views}
out=${1:-/Volumes/4/tmp/agenthail-collab/end-state}
mkdir -p "$out"
snap() { name=$1; shift; "$bin" --snap "$out/$name.png" --size 1280x800 --scale 2 --theme dark "$@" >/dev/null; }
snap 01-now-A --view now-a
snap 02-now-B --view now-b
snap 03-session --view session --session cd55560f-9682-4c82-b2da-7731e3d47dbb
snap 04-project-A --view project-a --project repo:github.com/zm2231/agenthail
snap 04b-project-A-crew --view project-a --project repo:github.com/storyboard-fs/contract-parser
snap 05-project-B --view project-b --project repo:github.com/zm2231/agenthail
snap 06-work --view work --item agenthail-W595
snap 07-jump --view jump
snap 08-session-ask --view session --session c1fafaa9-b321-4c5e-8895-dd8ad83cd9a3
ls "$out"
