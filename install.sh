#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${AGENTHAIL_INSTALL_DIR:-$HOME/.local/bin}"
REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "agenthail — building Go binary"
cd "$REPO_DIR"
go build -o agenthail ./cmd/agenthail

echo ""
echo "agenthail — installing sidecar deps (Claude send)"
cd "$REPO_DIR/sidecar"
npm install --silent 2>/dev/null || npm install

cd "$REPO_DIR"
mkdir -p "$INSTALL_DIR"
cp agenthail "$INSTALL_DIR/agenthail"
chmod +x "$INSTALL_DIR/agenthail"

# The sidecar runs from the repo so it can find its node_modules.
# The wrapper points the sidecar at the repo's cookie bridge + script.
WRAPPER="$INSTALL_DIR/claude-worker"
cat >"$WRAPPER" <<EOF
#!/usr/bin/env bash
export AGENTHAIL_COOKIE_BRIDGE="$REPO_DIR/sidecar/cookie.mjs"
exec python3 "$REPO_DIR/sidecar/claude-worker.py" "\$@"
EOF
chmod +x "$WRAPPER"

echo ""
echo "installed:"
echo "  $INSTALL_DIR/agenthail"
echo "  $INSTALL_DIR/claude-worker"
echo ""
echo "verify:  agenthail doctor"
echo ""
echo "for Codex support, launch Codex with:"
echo "  open -a Codex --args --inspect=127.0.0.1:9230 --remote-debugging-port=9231"
echo ""
echo "data dir: ~/.agenthail/ (registry.db, daemon.pid, daemon.log)"

if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo ""
  echo "NOTE: $INSTALL_DIR is not on your PATH. Add it:"
  echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi
