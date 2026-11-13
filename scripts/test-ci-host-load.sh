#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

mkdir -p "$work/bin" "$work/temp"
cat >"$work/bin/sysctl" <<'SH'
#!/usr/bin/env bash
case "$2" in
vm.loadavg) echo "{ 42.50 30.00 20.00 }" ;;
vm.swapusage) echo "total = 2048.00M  used = 512.25M  free = 1535.75M  (encrypted)" ;;
hw.model) echo Mac16,10 ;;
hw.ncpu) echo 10 ;;
hw.memsize) echo 68719476736 ;;
esac
SH
cat >"$work/bin/vm_stat" <<'SH'
#!/usr/bin/env bash
cat <<'EOF'
Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                              65536.
Pages active:                           655360.
Pages inactive:                         131072.
Pages speculative:                       65536.
Pages wired down:                       262144.
Pages purgeable:                         65536.
Pages occupied by compressor:           131072.
EOF
SH
cat >"$work/bin/ps" <<'SH'
#!/usr/bin/env bash
cat <<'EOF'
350.0 /usr/local/go/pkg/tool/darwin_arm64/compile
120.5 /private/var/folders/x/daemon.test
 60.0 /Library/Developer/CoreSimulator/Volumes/iOS_23A/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.0.simruntime/Contents/Resources/RuntimeRoot/sbin/launchd_sim
 55.0 /Library/Developer/CoreSimulator/Volumes/iOS_23A/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.0.simruntime/Contents/Resources/RuntimeRoot/sbin/launchd_sim
 25.0 /Library/Developer/CoreSimulator/Volumes/iOS_23A/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.0.simruntime/Contents/Resources/RuntimeRoot/System/Library/PrivateFrameworks/PosterKit.framework/KaleidoscopePoster
 40.0 /Users/runner/Library/Developer/CoreSimulator/Devices/ABC/data/Containers/Bundle/Application/DEF/Agenthail.app/Agenthail
 30.0 /opt/homebrew/bin/tart
 90.0 /System/Library/Frameworks/Virtualization.framework/XPCServices/com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine
 10.0 /Applications/Google Chrome Helper
EOF
SH
chmod +x "$work/bin/"*

run() {
	PATH="$work/bin:$PATH" RUNNER_TEMP="$work/temp" RUNNER_NAME=agenthail-test AGENTHAIL_LOAD_INTERVAL=0.1 "$ROOT/scripts/ci-host-load.sh" "$1"
}

run start
for _ in $(seq 50); do
	[ "$(wc -l <"$work/temp/host-load.tsv")" -ge 4 ] && break
	sleep 0.1
done
pid="$(cat "$work/temp/host-load.pid")"
case "$(ps -o args= -p "$pid")" in *"$ROOT/scripts/ci-host-load.sh loop"*) ;; *) fail "cleanup cannot match the sampler by its path: $(ps -o args= -p "$pid")" ;; esac
out="$(run report)"
! kill -0 "$pid" 2>/dev/null || fail "report left the sampler running"

grep -q '^host load on Mac16,10 10 cores 64 GB while agenthail-test ran: ' <<<"$out" || fail "no header in: $out"
grep -q 'load1 median 42.5, p90 42.5, max 42.5 (4.2 per core at max); used memory max 16.0 GB, available min 5.0 GB, swap max 512 MB; simulators max 2, VMs max 1' <<<"$out" || fail "wrong summary in: $out"
grep -q 'mean CPU among the top processes: compile 350%, daemon.test 120%, launchd_sim 115%, com.apple.Virtualization.VirtualMachine 90%, Agenthail 40%, tart 30%' <<<"$out" || fail "wrong processes in: $out"
grep -q '^mean CPU: all processes 780%, simulator services 140%, VMs 120%$' <<<"$out" || fail "wrong groups in: $out"

rm -f "$work/temp/host-load.pid"
summary() {
	head -2 "$work/temp/host-load.tsv" >"$work/temp/fixture"
	local t=1000 load
	for load in "$@"; do
		printf '%s\t%s\t1\t1\t0\t0\t0\t0\t0\tgo=1\n' "$((t += 10))" "$load" >>"$work/temp/fixture"
	done
	mv "$work/temp/fixture" "$work/temp/host-load.tsv"
	run report | sed -n 2p
}
summary 5 1 | grep -q '^load1 median 3.0, p90 5.0, max 5.0 ' || fail "two samples: $(summary 5 1)"
summary 9 1 8 2 7 3 6 4 5 10 | grep -q '^load1 median 5.5, p90 9.0, max 10.0 ' || fail "ten samples: $(summary 9 1 8 2 7 3 6 4 5 10)"
summary 3 1 2 | grep -q '^load1 median 2.0, p90 3.0, max 3.0 ' || fail "three samples: $(summary 3 1 2)"
summary 6 1 5 2 4 3 | grep -q '^load1 median 3.5, p90 6.0, max 6.0 ' || fail "six samples: $(summary 6 1 5 2 4 3)"

run start
pid="$(cat "$work/temp/host-load.pid")"
rm -f "$work/temp/host-load.tsv"
for _ in $(seq 50); do
	kill -0 "$pid" 2>/dev/null || break
	sleep 0.1
done
! kill -0 "$pid" 2>/dev/null || fail "the sampler outlived its samples file"

rm -f "$work/temp/host-load.pid"
run report | grep -q '^no host load samples$' || fail "a report without samples failed"
RUNNER_TEMP="$work/temp" "$ROOT/scripts/ci-host-load.sh" bogus 2>/dev/null && fail "an unknown command succeeded"

echo "ci-host-load tests passed"
