#!/usr/bin/env bash
set -euo pipefail

repo="zm2231/agenthail"
app_id=4268339
setec_url="https://secrets.tailb77c8a.ts.net"
key_name="agents/github/zm2231-agent-bot-private-key"
label="agenthail-vm"
slots="${AGENTHAIL_CI_VM_SLOTS:-2}"
cpu="${AGENTHAIL_CI_VM_CPU:-4}"
memory_mb="${AGENTHAIL_CI_VM_MEMORY_MB:-8192}"
min_free_gb=25
max_vm_minutes=50
idle_minutes=5
poll_seconds=20
state="$HOME/Library/Caches/agenthail-ci-vm"
self="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

log() { echo "$(date -u +%FT%TZ) $*"; }

free_gb() { df -g "$HOME" | awk 'NR == 2 { print $4 }'; }

image() {
	tart list --source local --format json |
		jq -r '.[].Name | select(test("^agenthail-ci-[0-9]{8}(-[0-9]+)?$"))' | sort | tail -1
}

slot_vms() {
	tart list --source local --format json | jq -r '.[].Name | select(test("^agenthail-ci-slot-[0-9]+$"))'
}

discard_vm() {
	tart stop "$1" >/dev/null 2>&1 || true
	tart delete "$1" >/dev/null 2>&1 || true
}

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

app_jwt() {
	local key header payload now signature
	key="$(curl -fsS -X POST -H 'Sec-X-Tailscale-No-Browsers: setec' -H 'Content-Type: application/json' \
		-d "{\"Name\":\"$key_name\"}" "$setec_url/api/get" | jq -er .Value | base64 -d)" || return 1
	now="$(date +%s)"
	header="$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)"
	payload="$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' $((now - 60)) $((now + 540)) "$app_id" | b64url)"
	signature="$(printf '%s.%s' "$header" "$payload" | openssl dgst -sha256 -sign <(printf '%s\n' "$key") | b64url)" || return 1
	printf '%s.%s.%s' "$header" "$payload" "$signature"
}

github() {
	local token="$1" method="$2" path="$3" body="${4:-}"
	curl -fsS -X "$method" -H "Authorization: Bearer $token" -H 'Accept: application/vnd.github+json' \
		${body:+-d "$body"} "https://api.github.com$path"
}

installation_token() {
	local permissions="$1" jwt id
	jwt="$(app_jwt)" || return 1
	id="$(github "$jwt" GET "/repos/$repo/installation" | jq -er .id)" || return 1
	github "$jwt" POST "/app/installations/$id/access_tokens" \
		"{\"repositories\":[\"${repo#*/}\"],\"permissions\":$permissions}" | jq -er .token
}

queued_jobs() {
	local token="$1" runs run count=0 n
	runs="$({
		github "$token" GET "/repos/$repo/actions/runs?status=queued&per_page=100"
		github "$token" GET "/repos/$repo/actions/runs?status=in_progress&per_page=100"
	} | jq -r '.workflow_runs[].id')" || return 1
	for run in $runs; do
		n="$(github "$token" GET "/repos/$repo/actions/runs/$run/jobs?filter=latest&per_page=100" |
			jq --arg label "$label" '[.jobs[] | select(.status == "queued" and (.labels | index($label)))] | length')" || return 1
		count=$((count + n))
	done
	echo "$count"
}

finish_slot() {
	discard_vm "$vm"
	if [ -n "$runner_id" ]; then
		github "$token" DELETE "/repos/$repo/actions/runners/$runner_id" >/dev/null 2>&1 || true
	fi
	rm -f "$state/slot-$slot.phase"
}

run_slot() {
	local base response jit started exec_pid run_pid phase=waiting
	base="$(image)"
	[ -n "$base" ] || { log "slot $slot: no agenthail-ci-YYYYMMDD image"; exit 1; }
	echo booting >"$state/slot-$slot.phase"
	discard_vm "$vm"
	tart clone "$base" "$vm"
	tart set "$vm" --cpu "$cpu" --memory "$memory_mb"
	tart run --no-graphics --net-softnet "$vm" >/dev/null 2>&1 &
	run_pid=$!
	for _ in $(seq 1 60); do
		tart exec "$vm" true >/dev/null 2>&1 && break
		sleep 2
	done
	tart exec "$vm" true >/dev/null

	token="$(installation_token '{"administration":"write"}')"
	response="$(github "$token" POST "/repos/$repo/actions/runners/generate-jitconfig" \
		"{\"name\":\"$vm-$(date +%s)\",\"runner_group_id\":1,\"labels\":[\"self-hosted\",\"macOS\",\"ARM64\",\"$label\"],\"work_folder\":\"_work\"}")"
	runner_id="$(jq -er .runner.id <<<"$response")"
	jit="$(jq -er .encoded_jit_config <<<"$response")"
	unset response
	echo waiting >"$state/slot-$slot.phase"
	log "slot $slot: $base booted, runner $runner_id waiting for a job"

	printf '%s\n' "$jit" | tart exec -i "$vm" /bin/bash -c 'read -r jit; PATH="$(cat ~/actions-runner/.path)" exec ~/actions-runner/run.sh --jitconfig "$jit"' >/dev/null 2>&1 &
	exec_pid=$!
	unset jit
	started="$(date +%s)"
	while kill -0 "$exec_pid" 2>/dev/null && kill -0 "$run_pid" 2>/dev/null; do
		if [ "$phase" = waiting ] && tart exec "$vm" pgrep -q Runner.Worker 2>/dev/null; then
			phase=busy
			echo busy >"$state/slot-$slot.phase"
			log "slot $slot: job started"
		fi
		if [ "$phase" = waiting ] && [ $(($(date +%s) - started)) -gt $((idle_minutes * 60)) ]; then
			log "slot $slot: no job within $idle_minutes minutes"
			return
		fi
		sleep 10
	done
	log "slot $slot: runner exited"
}

if [ "${1:-}" = run-slot ]; then
	slot="$2"
	vm="agenthail-ci-slot-$slot"
	token=""
	runner_id=""
	trap finish_slot EXIT
	trap 'exit 143' TERM INT
	run_slot
	exit
fi

declare -a worker_pid worker_started

reap() {
	local vm
	pkill -f "$self run-slot" 2>/dev/null || true
	for vm in $(slot_vms); do
		discard_vm "$vm"
	done
	rm -f "$state"/slot-*.phase
}

shutdown() {
	local slot
	for slot in $(seq 1 "$slots"); do
		[ -z "${worker_pid[slot]:-}" ] || kill "${worker_pid[slot]}" 2>/dev/null || true
	done
	sleep 15
	reap
	exit 0
}

mkdir -p "$state"
reap
trap shutdown TERM INT
log "supervising $slots slots ($cpu CPU, $memory_mb MB each)"

poll_token=""
poll_token_at=0
while true; do
	now="$(date +%s)"
	free_slots=""
	unclaimed=0
	for slot in $(seq 1 "$slots"); do
		pid="${worker_pid[slot]:-}"
		if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
			if [ $((now - worker_started[slot])) -gt $((max_vm_minutes * 60)) ]; then
				log "slot $slot: VM older than $max_vm_minutes minutes, discarding"
				kill "$pid" 2>/dev/null || true
				sleep 12
				kill -9 "$pid" 2>/dev/null || true
				discard_vm "agenthail-ci-slot-$slot"
				worker_pid[slot]=""
				free_slots="$free_slots $slot"
			elif [ "$(cat "$state/slot-$slot.phase" 2>/dev/null || echo busy)" != busy ]; then
				unclaimed=$((unclaimed + 1))
			fi
		else
			if [ -n "$pid" ]; then
				wait "$pid" 2>/dev/null || log "slot $slot: worker exited with an error"
				discard_vm "agenthail-ci-slot-$slot"
				worker_pid[slot]=""
			fi
			free_slots="$free_slots $slot"
		fi
	done

	if [ -n "$free_slots" ]; then
		if [ -z "$poll_token" ] || [ $((now - poll_token_at)) -gt 3000 ]; then
			if ! poll_token="$(installation_token '{"actions":"read"}')"; then
				log "could not mint a token"
				poll_token=""
			fi
			poll_token_at="$now"
		fi
		if [ -n "$poll_token" ] && queued="$(queued_jobs "$poll_token")"; then
			want=$((queued - unclaimed))
			for slot in $free_slots; do
				[ "$want" -gt 0 ] || break
				if [ "$(free_gb)" -lt "$min_free_gb" ]; then
					log "refusing to clone: $(free_gb) GB free, need $min_free_gb"
					break
				fi
				"$self" run-slot "$slot" &
				worker_pid[slot]=$!
				worker_started[slot]="$now"
				want=$((want - 1))
				log "slot $slot: starting for a queued job"
			done
		elif [ -n "$poll_token" ]; then
			log "could not list queued jobs"
			poll_token=""
		fi
	fi
	sleep "$poll_seconds" &
	wait $! || true
done
