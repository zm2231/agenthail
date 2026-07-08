#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${AGENTHAIL_INSTALL_DIR:-$HOME/.local/bin}"
DATA_DIR="${AGENTHAIL_DATA_DIR:-$HOME/.local/share/agenthail}"
REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "agenthail — building Go binary"
cd "$REPO_DIR"
go build -o agenthail ./cmd/agenthail

echo ""
echo "agenthail — installing sidecar deps (curl_cffi, sweet-cookie)"
cd "$REPO_DIR/sidecar"
npm install --silent 2>/dev/null || npm install

cd "$REPO_DIR"

echo ""
echo "agenthail — installing to $DATA_DIR"
mkdir -p "$DATA_DIR"
# Copy the Go binary
cp agenthail "$DATA_DIR/agenthail"
chmod +x "$DATA_DIR/agenthail"

# Copy sidecar files alongside the binary (transport resolves them via sibling lookup)
cp sidecar/sidecar.py "$DATA_DIR/sidecar.py"
cp sidecar/cookie.mjs "$DATA_DIR/cookie.mjs"

# Install curl_cffi into the data dir for the sidecar
pip3 install --target "$DATA_DIR/pydeps" --quiet curl_cffi 2>/dev/null || pip3 install --target "$DATA_DIR/pydeps" curl_cffi

# Copy node_modules so cookie bridge works without the repo
if [ -d "$REPO_DIR/sidecar/node_modules" ]; then
  cp -R "$REPO_DIR/sidecar/node_modules" "$DATA_DIR/node_modules"
fi

# Also copy package.json so node can resolve modules
cp sidecar/package.json "$DATA_DIR/package.json"

echo ""
echo "creating wrapper at $INSTALL_DIR/agenthail"

# Create a wrapper in ~/.local/bin that sets env vars and execs the binary.
# Both the binary and all sidecar files live in $DATA_DIR so sibling lookup works.
mkdir -p "$INSTALL_DIR"
cat >"$INSTALL_DIR/agenthail" <<EOF
#!/usr/bin/env bash
export AGENTHAIL_SIDECAR="$DATA_DIR/sidecar.py"
export AGENTHAIL_COOKIE_BRIDGE="$DATA_DIR/cookie.mjs"
export PYTHONPATH="$DATA_DIR/pydeps:\$PYTHONPATH"
exec "$DATA_DIR/agenthail" "\$@"
EOF
chmod +x "$INSTALL_DIR/agenthail"

# Remove old claude-worker wrapper if present
rm -f "$INSTALL_DIR/claude-worker" 2>/dev/null || true

echo ""
echo "installed: agenthail $INSTALL_DIR/agenthail"
echo "  sidecar:  $DATA_DIR/sidecar.py"
echo "  cookies:  $DATA_DIR/cookie.mjs"
echo ""
echo "verify:    agenthail doctor"
echo ""
echo "for Codex support:"
echo "  agenthail launch codex"
echo "  (or: open -a Codex --args --inspect=127.0.0.1:9230 --remote-debugging-port=9231)"
echo ""
echo "data dir:  ~/.agenthail/ (registry.db, daemon.pid, daemon.log)"

if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo ""
  echo "NOTE: $INSTALL_DIR is not on your PATH. Add it:"
  echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi
