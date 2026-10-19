#!/usr/bin/env bash
set -euo pipefail

slot="${1:?usage: supervisor.sh <slot>}"
mode="${2:-loop}"
repo="zm2231/agenthail"
app_id=4268339
setec_url="https://secrets.tailb77c8a.ts.net"
key_name="agents/github/zm2231-agent-bot-private-key"
labels='["self-hosted","macOS","ARM64","agenthail-vm"]'
min_free_gb=25
max_job_minutes=90
vm="agenthail-ci-slot-$slot"
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

log() { echo "$(date -u +%FT%TZ) slot $slot: $*"; }

free_gb() { df -g "$HOME" | awk 'NR == 2 { print $4 }'; }

image() {
	tart list --source local --format json |
		jq -r '.[].Name | select(test("^agenthail-ci-[0-9]{8}(-[0-9]+)?$"))' | sort | tail -1
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
	local jwt id
	jwt="$(app_jwt)" || return 1
	id="$(github "$jwt" GET "/repos/$repo/installation" | jq -er .id)" || return 1
	github "$jwt" POST "/app/installations/$id/access_tokens" \
		"{\"repositories\":[\"${repo#*/}\"],\"permissions\":{\"administration\":\"write\"}}" | jq -er .token
}

jit_config() {
	local token="$1" name
	name="agenthail-vm-$slot-$(date +%s)"
	github "$token" POST "/repos/$repo/actions/runners/generate-jitconfig" \
		"{\"name\":\"$name\",\"runner_group_id\":1,\"labels\":$labels,\"work_folder\":\"_work\"}" |
		jq -er .encoded_jit_config
}

remove_stale_runners() {
	local token="$1" ids id
	ids="$(github "$token" GET "/repos/$repo/actions/runners?per_page=100" |
		jq -r --arg prefix "agenthail-vm-$slot-" '.runners[] | select((.name | startswith($prefix)) and .status == "offline") | .id')" || return 1
	for id in $ids; do
		github "$token" DELETE "/repos/$repo/actions/runners/$id" >/dev/null || true
	done
}

discard_vm() {
	tart stop "$vm" >/dev/null 2>&1 || true
	tart delete "$vm" >/dev/null 2>&1 || true
}

worker_running() { tart exec "$vm" pgrep -q Runner.Worker 2>/dev/null; }

run_one_job() {
	local base token jit exec_pid run_pid busy_since=0
	base="$(image)"
	if [ -z "$base" ]; then
		log "no agenthail-ci-YYYYMMDD image"
		exit 1
	fi
	if [ "$(free_gb)" -lt "$min_free_gb" ]; then
		log "refusing to clone: $(free_gb) GB free, need $min_free_gb"
		exit 1
	fi

	discard_vm
	tart clone "$base" "$vm"
	tart run --no-graphics --net-softnet "$vm" >/dev/null 2>&1 &
	run_pid=$!
	for _ in $(seq 1 60); do
		tart exec "$vm" true >/dev/null 2>&1 && break
		sleep 2
	done
	tart exec "$vm" true >/dev/null

	if ! token="$(installation_token)" || ! remove_stale_runners "$token" || ! jit="$(jit_config "$token")"; then
		log "could not mint a JIT runner config"
		exit 1
	fi
	unset token
	log "$base ready, waiting for a job"

	printf '%s\n' "$jit" | tart exec -i "$vm" /bin/bash -c 'read -r jit; exec ~/actions-runner/run.sh --jitconfig "$jit"' &
	exec_pid=$!
	unset jit

	while kill -0 "$exec_pid" 2>/dev/null; do
		if worker_running; then
			[ "$busy_since" -gt 0 ] || busy_since="$(date +%s)"
			if [ $(($(date +%s) - busy_since)) -gt $((max_job_minutes * 60)) ]; then
				log "job ran over $max_job_minutes minutes, discarding the VM"
				break
			fi
		fi
		kill -0 "$run_pid" 2>/dev/null || { log "VM exited"; break; }
		sleep 15
	done
	kill "$exec_pid" 2>/dev/null || true
	wait "$exec_pid" 2>/dev/null || true
	discard_vm
	wait "$run_pid" 2>/dev/null || true
	log "job finished, VM discarded"
}

if [ "$mode" = once ]; then
	trap discard_vm EXIT
	trap 'exit 143' TERM INT
	run_one_job
	exit
fi

while true; do
	"$0" "$slot" once || { log "attempt failed"; sleep 300; }
done
