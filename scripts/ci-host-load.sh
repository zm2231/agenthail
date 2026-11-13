#!/usr/bin/env bash
# Usage: ci-host-load.sh start|report
# Samples the whole host's load, memory and busiest processes during a job; never fails it.
set -uo pipefail

: "${RUNNER_TEMP:?}"
samples="$RUNNER_TEMP/host-load.tsv"
pidfile="$RUNNER_TEMP/host-load.pid"
interval="${AGENTHAIL_LOAD_INTERVAL:-10}"

sample() {
	local load swap vm procs
	load="$(sysctl -n vm.loadavg | tr -d '{}' | awk '{print $1}')"
	swap="$(sysctl -n vm.swapusage | sed -n 's/.*used = \([0-9.]*\)M.*/\1/p')"
	vm="$(vm_stat | awk '
		/page size of/ { size = $8 }
		/^Pages (free|inactive|speculative|purgeable)/ { gsub(/\./, "", $NF); avail += $NF }
		/^Pages (active|wired down)/ { gsub(/\./, "", $NF); used += $NF }
		/^Pages occupied by compressor/ { gsub(/\./, "", $NF); used += $NF; comp = $NF }
		END { g = size / 1073741824; printf "%.1f\t%.1f\t%.1f", used * g, avail * g, comp * g }')"
	procs="$(ps -axo pcpu=,comm= | awk '
		{ pct = $1; $1 = ""; name = substr($0, 2); sub(/.*\//, "", name); cpu[name] += pct; total += pct }
		name == "launchd_sim" { sims++ }
		name ~ /Virtualization.VirtualMachine/ { vms++ }
		END {
			n = 0
			for (k in cpu) order[++n] = k
			for (i = 1; i <= n; i++) for (j = i + 1; j <= n; j++) if (cpu[order[j]] > cpu[order[i]]) { t = order[i]; order[i] = order[j]; order[j] = t }
			top = ""
			for (i = 1; i <= n && i <= 6; i++) top = top (i > 1 ? "," : "") order[i] "=" int(cpu[order[i]])
			printf "%d\t%d\t%d\t%s", total, sims, vms, top
		}')"
	printf '%s\t%s\t%s\t%s\t%s\n' "$(date +%s)" "$load" "$vm" "${swap:-0}" "$procs"
}

start() {
	{
		printf 'host\t%s\t%s cores\t%s GB\n' "$(sysctl -n hw.model)" "$(sysctl -n hw.ncpu)" "$(($(sysctl -n hw.memsize) / 1073741824))"
		printf 'time\tload1\tused_gb\tavailable_gb\tcompressed_gb\tswap_mb\tcpu_pct\tsimulators\tvms\ttop_cpu_pct\n'
	} >"$samples"
	(
		while :; do
			sample >>"$samples" 2>/dev/null
			sleep "$interval"
		done
	) </dev/null >/dev/null 2>&1 &
	echo $! >"$pidfile"
}

report() {
	[ ! -f "$pidfile" ] || kill "$(cat "$pidfile")" 2>/dev/null
	[ -f "$samples" ] || { echo "no host load samples"; return 0; }
	awk -F '\t' -v runner="${RUNNER_NAME:-}" '
		NR == 1 { host = $2 " " $3 " " $4; cores = $3 + 0; next }
		NR == 2 { next }
		{
			n++; load[n] = $2; if ($2 > maxload) maxload = $2
			if (n == 1 || $4 < minavail) minavail = $4
			if ($3 > maxused) maxused = $3
			if ($6 > maxswap) maxswap = $6
			if ($8 > maxsims) maxsims = $8
			if ($9 > maxvms) maxvms = $9
			if (n == 1) first = $1; last = $1
			split($10, top, ",")
			for (i in top) { split(top[i], kv, "="); share[kv[1]] += kv[2] }
		}
		END {
			if (!n) { print "no host load samples"; exit }
			for (i = 1; i <= n; i++) for (j = i + 1; j <= n; j++) if (load[j] < load[i]) { t = load[i]; load[i] = load[j]; load[j] = t }
			printf "host load on %s while %s ran: %d samples over %d s\n", host, runner, n, last - first
			p90 = int(n * 0.9 + 0.5)
			if (p90 < 1) p90 = 1
			percore = cores ? maxload / cores : 0
			printf "load1 median %.0f, p90 %.0f, max %.0f (%.1f per core at max); used memory max %.1f GB, available min %.1f GB, swap max %.0f MB; simulators max %d, VMs max %d\n", load[int((n + 1) / 2)], load[p90], maxload, percore, maxused, minavail, maxswap, maxsims, maxvms
			m = 0
			for (k in share) order[++m] = k
			for (i = 1; i <= m; i++) for (j = i + 1; j <= m; j++) if (share[order[j]] > share[order[i]]) { t = order[i]; order[i] = order[j]; order[j] = t }
			line = ""
			for (i = 1; i <= m && i <= 8; i++) line = line (i > 1 ? ", " : "") order[i] " " int(share[order[i]] / n) "%"
			print "mean CPU among the top processes: " line
		}' "$samples"
	echo "::group::host load samples"
	cat "$samples"
	echo "::endgroup::"
}

case "${1:-}" in
	start) start ;;
	report) report ;;
	*) echo "usage: $0 start|report" >&2; exit 2 ;;
esac
exit 0
