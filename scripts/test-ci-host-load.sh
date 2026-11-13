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
 60.0 /Library/Developer/CoreSimulator/launchd_sim
 55.0 /Library/Developer/CoreSimulator/launchd_sim
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
out="$(run report)"
pid="$(cat "$work/temp/host-load.pid")"
! kill -0 "$pid" 2>/dev/null || fail "report left the sampler running"

grep -q '^host load on Mac16,10 10 cores 64 GB while agenthail-test ran: ' <<<"$out" || fail "no header in: $out"
grep -q 'load1 median 42, p90 42, max 42 (4.2 per core at max); used memory max 16.0 GB, available min 5.0 GB, swap max 512 MB; simulators max 2, VMs max 1' <<<"$out" || fail "wrong summary in: $out"
grep -q 'mean CPU among the top processes: compile 350%, daemon.test 120%, launchd_sim 115%, com.apple.Virtualization.VirtualMachine 90%, Google Chrome Helper 10%' <<<"$out" || fail "wrong processes in: $out"

rm -f "$work/temp/host-load.tsv" "$work/temp/host-load.pid"
run report | grep -q '^no host load samples$' || fail "a report without samples failed"
RUNNER_TEMP="$work/temp" "$ROOT/scripts/ci-host-load.sh" bogus 2>/dev/null && fail "an unknown command succeeded"

echo "ci-host-load tests passed"
