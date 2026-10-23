#!/usr/bin/env bash
# Serves the pinned Node archive through a local server that breaks some
# transfers, into a cache whose path has a quote, a dollar and a backslash.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-fetch-node.XXXXXX")"
server_pid=""
cleanup() {
	[ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
	chmod -R u+w "$TMP" 2>/dev/null || true
	rm -rf "$TMP"
}
trap cleanup EXIT

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"
dist="$("$ROOT/scripts/fetch-node.sh" --dist "$os" "$arch")"
archive="$dist.tar.gz"
test -f "$archive"

cat >"$TMP/server.mjs" <<'EOF'
import { createServer } from "node:http";
import { readFileSync, writeFileSync, appendFileSync } from "node:fs";

const [archive, modeFile, logFile, portFile] = process.argv.slice(2);
const body = readFileSync(archive);
const server = createServer((request, response) => {
	const faults = readFileSync(modeFile, "utf8").trim().split(/\s+/).filter(Boolean);
	const fault = faults.shift() ?? "ok";
	writeFileSync(modeFile, faults.join(" "));
	appendFileSync(logFile, `${request.url} ${fault}\n`);
	response.writeHead(200, { "content-type": "application/gzip", "content-length": body.length });
	if (fault === "drop") {
		response.write(body.subarray(0, body.length >> 1), () => request.socket.destroy());
		return;
	}
	if (fault === "corrupt") {
		const bad = Buffer.from(body);
		bad[bad.length >> 1] ^= 0xff;
		response.end(bad);
		return;
	}
	response.end(body);
});
server.listen(0, "127.0.0.1", () => writeFileSync(portFile, String(server.address().port)));
EOF

: >"$TMP/faults"
: >"$TMP/requests"
node "$TMP/server.mjs" "$archive" "$TMP/faults" "$TMP/requests" "$TMP/port" &
server_pid=$!
for _ in $(seq 50); do
	[ -s "$TMP/port" ] && break
	sleep 0.1
done
mirror="http://127.0.0.1:$(cat "$TMP/port")"
odd="$TMP/odd\" dollar\$ back\\slash"

fetch() {
	AGENTHAIL_BUILD_CACHE="$odd/cache" AGENTHAIL_NODE_DIST_URL="$mirror" \
		"$ROOT/scripts/fetch-node.sh" "$os" "$arch" "$odd/root"
}

expect_staged() {
	test "$("$odd/root/runtime/node/bin/node" --version)" = "$(basename "$dist" | cut -d- -f2)"
	test "$(ls "$odd/cache" | grep -c '\.tar\.gz')" = 1
}

requests() {
	wc -l <"$TMP/requests" | tr -d ' '
}

echo drop corrupt >"$TMP/faults"
fetch 2>"$TMP/broken.err"
expect_staged
grep -q 'attempt 1 of 4 .* failed (curl exit [1-9][0-9]*, got [0-9a-f]\{64\}, want ' "$TMP/broken.err"
grep -q 'attempt 2 of 4 .* failed (curl exit 0, got [0-9a-f]\{64\}, want ' "$TMP/broken.err"
test "$(requests)" = 3

before="$(requests)"
fetch
expect_staged
test "$(requests)" = "$before"

rm -rf "$odd/cache" "$odd/root"
echo corrupt corrupt corrupt corrupt corrupt corrupt >"$TMP/faults"
before="$(requests)"
if fetch 2>"$TMP/refused.err"; then
	echo "fetch-node accepted an archive that never matched its checksum" >&2
	exit 1
fi
attempts=$(($(requests) - before))
test "$attempts" = 4
test "$(grep -c 'failed (curl exit 0, got [0-9a-f]\{64\}, want [0-9a-f]\{64\})' "$TMP/refused.err")" = 4
test ! -e "$odd/root/runtime/node/bin/node"
test -z "$(ls "$odd/cache" 2>/dev/null | grep '\.tar\.gz')"

echo "fetch-node retries broken downloads and verifies under any cache path"
