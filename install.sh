#!/usr/bin/env bash
set -euo pipefail

INSTALL_SKILL="${AGENTHAIL_INSTALL_SKILL:-1}"
for arg in "$@"; do
	case "$arg" in
		--no-skill) INSTALL_SKILL=0 ;;
		--with-skill) INSTALL_SKILL=1 ;;
		-h|--help)
			cat <<'USAGE'
usage: ./install.sh [--no-skill]

  --no-skill   do not link the agenthail-operations skill into ~/.claude/skills
               or ~/.codex/skills (they are only touched if they already exist)

environment:
  AGENTHAIL_INSTALL_DIR   directory for the agenthail wrapper
  AGENTHAIL_DATA_DIR      directory for the binary and sidecar
  AGENTHAIL_PYTHON        absolute path to a Python 3.10+ interpreter
  AGENTHAIL_VERSION       override the stamped version string
USAGE
			exit 0
			;;
		*)
			echo "error: unknown argument $arg (try --help)" >&2
			exit 1
			;;
	esac
done

DEFAULT_INSTALL_DIR="$HOME/.local/bin"
if [ -d /opt/homebrew/bin ] && [ -w /opt/homebrew/bin ]; then
	DEFAULT_INSTALL_DIR=/opt/homebrew/bin
elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
	DEFAULT_INSTALL_DIR=/usr/local/bin
fi
INSTALL_DIR="${AGENTHAIL_INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"
DATA_DIR="${AGENTHAIL_DATA_DIR:-$HOME/.local/share/agenthail}"
REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
RESTART_DAEMON=0
REINSTALL_SERVICE=0
RUNTIME_STOPPED=0
ACTIVATED=0
INSTALL_SUCCEEDED=0
SERVICE_PLIST="$HOME/Library/LaunchAgents/com.agenthail.daemon.plist"
LEGACY_MENUBAR_PLIST="$HOME/Library/LaunchAgents/com.agenthail.menubar.plist"
PARENT_DIR="$(dirname "$DATA_DIR")"
mkdir -p "$PARENT_DIR"
STAGE_DIR="$(mktemp -d "$PARENT_DIR/.agenthail-stage.XXXXXX")"
NEXT_DIR="$DATA_DIR.next"
PREVIOUS_DIR="$DATA_DIR.previous"
TARGET_WRAPPER="$INSTALL_DIR/agenthail"
TARGET_WRAPPER_OWNED=0
TARGET_WRAPPER_EXISTED=0
TARGET_WRAPPER_TOUCHED=0
TARGET_WRAPPER_BACKUP=""
printf -v DATA_BINARY_SHELL '%q' "$DATA_DIR/agenthail"
OWNED_WRAPPERS=()
for candidate in "$INSTALL_DIR/agenthail" "$HOME/.local/bin/agenthail" /opt/homebrew/bin/agenthail /usr/local/bin/agenthail; do
	if [ ! -f "$candidate" ]; then
		continue
	fi
	if ! grep -Fq "exec \"$DATA_DIR/agenthail\"" "$candidate" 2>/dev/null && \
	   ! grep -Fq "exec $DATA_BINARY_SHELL " "$candidate" 2>/dev/null; then
		continue
	fi
	duplicate=0
	if [ "${#OWNED_WRAPPERS[@]}" -gt 0 ]; then
		for wrapper in "${OWNED_WRAPPERS[@]}"; do
			if [ "$wrapper" = "$candidate" ]; then
				duplicate=1
				break
			fi
		done
	fi
	if [ "$duplicate" -eq 0 ]; then
		OWNED_WRAPPERS+=("$candidate")
	fi
	if [ "$candidate" = "$TARGET_WRAPPER" ]; then
		TARGET_WRAPPER_OWNED=1
	fi
done
if [ -e "$TARGET_WRAPPER" ] && [ "$TARGET_WRAPPER_OWNED" -ne 1 ]; then
	rm -rf "$STAGE_DIR"
	echo "error: refusing to overwrite unmanaged executable $TARGET_WRAPPER; choose another AGENTHAIL_INSTALL_DIR or remove it explicitly" >&2
	exit 1
fi
EXISTING_WRAPPER=""
if [ "${#OWNED_WRAPPERS[@]}" -gt 0 ]; then
	EXISTING_WRAPPER="${OWNED_WRAPPERS[0]}"
fi
cleanup() {
	exit_code=$?
	rm -rf "$STAGE_DIR" "$NEXT_DIR"
	if [ "$INSTALL_SUCCEEDED" -ne 1 ]; then
		if [ "$ACTIVATED" -eq 1 ]; then
			rm -rf "$DATA_DIR"
			if [ -e "$PREVIOUS_DIR" ]; then
				mv "$PREVIOUS_DIR" "$DATA_DIR"
			fi
		fi
		if [ "$TARGET_WRAPPER_TOUCHED" -eq 1 ]; then
			if [ "$TARGET_WRAPPER_EXISTED" -eq 1 ] && [ -f "$TARGET_WRAPPER_BACKUP" ]; then
				cp "$TARGET_WRAPPER_BACKUP" "$TARGET_WRAPPER"
				chmod +x "$TARGET_WRAPPER"
			else
				rm -f "$TARGET_WRAPPER"
			fi
		fi
		if [ "$RUNTIME_STOPPED" -eq 1 ]; then
			if [ "$REINSTALL_SERVICE" -eq 1 ] && [ -f "$SERVICE_PLIST" ]; then
				launchctl bootstrap "gui/$UID" "$SERVICE_PLIST" >/dev/null 2>&1 || true
				launchctl kickstart -k "gui/$UID/com.agenthail.daemon" >/dev/null 2>&1 || true
			elif [ "$RESTART_DAEMON" -eq 1 ]; then
				control_wrapper="$INSTALL_DIR/agenthail"
				if [ ! -x "$control_wrapper" ] && [ -n "$EXISTING_WRAPPER" ]; then
					control_wrapper="$EXISTING_WRAPPER"
				fi
				[ -x "$control_wrapper" ] && "$control_wrapper" daemon start >/dev/null 2>&1 || true
			fi
		fi
	fi
	rm -f "$TARGET_WRAPPER_BACKUP"
	return "$exit_code"
}
trap cleanup EXIT

python_is_supported() {
	"$1" -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)' >/dev/null 2>&1
}

PYTHON_BIN=""
if [ -n "${AGENTHAIL_PYTHON:-}" ]; then
	if [ ! -x "$AGENTHAIL_PYTHON" ] || ! python_is_supported "$AGENTHAIL_PYTHON"; then
		echo "error: AGENTHAIL_PYTHON must point to Python 3.10+ (got $AGENTHAIL_PYTHON)" >&2
		exit 1
	fi
	PYTHON_BIN="$AGENTHAIL_PYTHON"
else
	for candidate in python3.14 python3.13 python3.12 python3.11 python3.10 python3; do
		candidate_path="$(command -v "$candidate" 2>/dev/null || true)"
		if [ -n "$candidate_path" ] && python_is_supported "$candidate_path"; then
			PYTHON_BIN="$candidate_path"
			break
		fi
	done
fi
if [ -z "$PYTHON_BIN" ]; then
	echo "error: Python 3.10+ is required; set AGENTHAIL_PYTHON to its absolute path" >&2
	exit 1
fi
PYTHON_BIN="$(cd "$(dirname "$PYTHON_BIN")" && pwd)/$(basename "$PYTHON_BIN")"

echo "agenthail: building Go binary"
cd "$REPO_DIR"
if [ -n "${AGENTHAIL_PREBUILT_BINARY:-}" ]; then
	cp "$AGENTHAIL_PREBUILT_BINARY" "$STAGE_DIR/agenthail"
elif [ -d "$REPO_DIR/cmd/agenthail" ]; then
	BUILD_VERSION="${AGENTHAIL_VERSION:-dev}"
	BUILD_REVISION="unknown"
	BUILD_AT=""
	if git -C "$REPO_DIR" rev-parse --git-dir >/dev/null 2>&1; then
		BUILD_REVISION="$(git -C "$REPO_DIR" rev-parse HEAD)"
		if [ -z "${AGENTHAIL_VERSION:-}" ]; then
			BUILD_VERSION="$(git -C "$REPO_DIR" describe --tags --always 2>/dev/null || echo dev)"
		fi
		if [ -n "$(git -C "$REPO_DIR" status --porcelain 2>/dev/null)" ]; then
			BUILD_VERSION="$BUILD_VERSION-dirty"
		fi
		BUILD_AT="$(date -u -r "$(git -C "$REPO_DIR" show -s --format=%ct HEAD)" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || true)"
	fi
	go build -trimpath \
		-ldflags="-s -w -X main.version=$BUILD_VERSION -X main.revision=$BUILD_REVISION -X main.builtAt=$BUILD_AT" \
		-o "$STAGE_DIR/agenthail" ./cmd/agenthail
elif [ -x "$REPO_DIR/agenthail" ]; then
	cp "$REPO_DIR/agenthail" "$STAGE_DIR/agenthail"
else
	echo "error: no Go source tree or prebuilt agenthail binary found" >&2
	exit 1
fi

echo ""
echo "agenthail: installing sidecar deps (curl_cffi, sweet-cookie)"
echo "agenthail: Python runtime: $PYTHON_BIN ($("$PYTHON_BIN" --version 2>&1))"
cd "$REPO_DIR/sidecar"
npm ci --silent 2>/dev/null || npm ci

cd "$REPO_DIR"

echo ""
echo "agenthail: installing to $DATA_DIR"
chmod +x "$STAGE_DIR/agenthail"
codesign --verify "$STAGE_DIR/agenthail" 2>/dev/null || codesign --force --sign - "$STAGE_DIR/agenthail" 2>/dev/null || true
cp sidecar/sidecar.py "$STAGE_DIR/sidecar.py"
cp sidecar/cookie.mjs "$STAGE_DIR/cookie.mjs"
if [ -d "$REPO_DIR/skills" ]; then
	cp -R "$REPO_DIR/skills" "$STAGE_DIR/skills"
fi

"$PYTHON_BIN" -m pip install --target "$STAGE_DIR/pydeps" --quiet "${AGENTHAIL_CURL_CFFI_SPEC:-curl_cffi==0.15.0}" 2>/dev/null || \
  "$PYTHON_BIN" -m pip install --target "$STAGE_DIR/pydeps" "${AGENTHAIL_CURL_CFFI_SPEC:-curl_cffi==0.15.0}"

if [ -d "$REPO_DIR/sidecar/node_modules" ]; then
	mkdir -p "$STAGE_DIR/node_modules"
	cp -R "$REPO_DIR/sidecar/node_modules/." "$STAGE_DIR/node_modules/"
fi

cp sidecar/package.json sidecar/package-lock.json "$STAGE_DIR/"

SERVICE_PROGRAM=""
SERVICE_MANAGED=0
if [ -f "$SERVICE_PLIST" ]; then
	SERVICE_PROGRAM="$(plutil -extract ProgramArguments.0 raw -o - "$SERVICE_PLIST" 2>/dev/null || true)"
fi
if [ -n "$SERVICE_PROGRAM" ]; then
	if [ "$SERVICE_PROGRAM" = "$DATA_DIR/agenthail" ]; then
		SERVICE_MANAGED=1
	else
		printf -v SERVICE_PROGRAM_SHELL '%q' "$SERVICE_PROGRAM"
		if [ "${#OWNED_WRAPPERS[@]}" -gt 0 ]; then
			for wrapper in "${OWNED_WRAPPERS[@]}"; do
				if grep -Fq "exec \"$SERVICE_PROGRAM\"" "$wrapper" 2>/dev/null || grep -Fq "exec $SERVICE_PROGRAM_SHELL " "$wrapper" 2>/dev/null; then
					SERVICE_MANAGED=1
					break
				fi
			done
		fi
	fi
fi
if [ -f "$SERVICE_PLIST" ] && [ "$SERVICE_MANAGED" -eq 1 ]; then
	echo "agenthail: stopping supervised daemon for upgrade"
	launchctl bootout "gui/$UID/com.agenthail.daemon" >/dev/null 2>&1 || true
	REINSTALL_SERVICE=1
	RUNTIME_STOPPED=1
elif [ -f "$SERVICE_PLIST" ]; then
	echo "agenthail: leaving unrelated supervised daemon unchanged ($SERVICE_PROGRAM)"
elif [ -n "$EXISTING_WRAPPER" ] && [ -x "$EXISTING_WRAPPER" ]; then
	DAEMON_STATUS="$("$EXISTING_WRAPPER" daemon status 2>/dev/null || true)"
	case "$DAEMON_STATUS" in
		*"daemon: running"*)
			echo "agenthail: stopping running daemon for upgrade"
			"$EXISTING_WRAPPER" daemon stop
			RESTART_DAEMON=1
			RUNTIME_STOPPED=1
			;;
	esac
fi

rm -rf "$NEXT_DIR" "$PREVIOUS_DIR"
mv "$STAGE_DIR" "$NEXT_DIR"
ACTIVATED=1
if [ -e "$DATA_DIR" ]; then
	mv "$DATA_DIR" "$PREVIOUS_DIR"
fi
mv "$NEXT_DIR" "$DATA_DIR"

echo ""
echo "creating wrapper at $INSTALL_DIR/agenthail"

# Create a wrapper in the selected command directory that sets env vars and execs the binary.
# Both the binary and all sidecar files live in $DATA_DIR, so sibling lookup works.
mkdir -p "$INSTALL_DIR"
if [ -e "$TARGET_WRAPPER" ]; then
	TARGET_WRAPPER_EXISTED=1
	TARGET_WRAPPER_BACKUP="$(mktemp "$PARENT_DIR/.agenthail-wrapper.XXXXXX")"
	cp "$TARGET_WRAPPER" "$TARGET_WRAPPER_BACKUP"
fi
TARGET_WRAPPER_TOUCHED=1
printf -v SIDECAR_SHELL '%q' "$DATA_DIR/sidecar.py"
printf -v COOKIE_BRIDGE_SHELL '%q' "$DATA_DIR/cookie.mjs"
printf -v PYTHON_SHELL '%q' "$PYTHON_BIN"
printf -v PYDEPS_SHELL '%q' "$DATA_DIR/pydeps"
cat >"$INSTALL_DIR/agenthail" <<EOF
#!/usr/bin/env bash
# agenthail-managed-wrapper-v1
export AGENTHAIL_SIDECAR=$SIDECAR_SHELL
export AGENTHAIL_COOKIE_BRIDGE=$COOKIE_BRIDGE_SHELL
export AGENTHAIL_PYTHON=$PYTHON_SHELL
export PYTHONPATH=$PYDEPS_SHELL:\${PYTHONPATH:-}
exec $DATA_BINARY_SHELL "\$@"
EOF
chmod +x "$INSTALL_DIR/agenthail"

if [ "${AGENTHAIL_INSTALL_TEST_FAIL_AFTER_ACTIVATION:-0}" = "1" ]; then
	echo "error: injected post-activation failure" >&2
	false
fi

if [ "$REINSTALL_SERVICE" -eq 1 ]; then
	echo "agenthail: reinstalling supervised daemon"
	"$INSTALL_DIR/agenthail" daemon install
elif [ "$RESTART_DAEMON" -eq 1 ]; then
	echo "agenthail: restarting daemon with the upgraded binary"
	"$INSTALL_DIR/agenthail" daemon start
fi

if [ "${#OWNED_WRAPPERS[@]}" -gt 0 ]; then
	for wrapper in "${OWNED_WRAPPERS[@]}"; do
		if [ "$wrapper" != "$INSTALL_DIR/agenthail" ]; then
			rm -f "$wrapper"
		fi
	done
fi

LEGACY_APP="$PREVIOUS_DIR/Agenthail.app/Contents/MacOS/Agenthail"
if [ -x "$LEGACY_APP" ]; then
	"$LEGACY_APP" service disable >/dev/null 2>&1 || true
fi
launchctl bootout "gui/$UID/com.agenthail.menubar" >/dev/null 2>&1 || true
rm -f "$LEGACY_MENUBAR_PLIST" "$HOME/.agenthail/notifications.json"
pkill -f '/Agenthail.app/Contents/MacOS/Agenthail' >/dev/null 2>&1 || true

INSTALL_SUCCEEDED=1
rm -rf "$PREVIOUS_DIR"
rm -f "$TARGET_WRAPPER_BACKUP"
trap - EXIT

# Remove old claude-worker wrapper if present
rm -f "$INSTALL_DIR/claude-worker" 2>/dev/null || true

SKILL_SOURCE="$DATA_DIR/skills/agenthail-operations"
SKILL_LINKS=()
if [ "$INSTALL_SKILL" -eq 1 ] && [ -f "$SKILL_SOURCE/SKILL.md" ]; then
	for runtime_dir in "$HOME/.claude" "$HOME/.codex"; do
		[ -d "$runtime_dir" ] || continue
		link="$runtime_dir/skills/agenthail-operations"
		if [ -e "$link" ] && [ ! -L "$link" ]; then
			echo "warning: $link exists and is not an agenthail symlink; leaving it alone" >&2
			continue
		fi
		mkdir -p "$runtime_dir/skills"
		ln -sfn "$SKILL_SOURCE" "$link"
		SKILL_LINKS+=("$link")
	done
fi

echo ""
echo "installed: agenthail $INSTALL_DIR/agenthail"
echo "  sidecar:  $DATA_DIR/sidecar.py"
echo "  python:   $PYTHON_BIN"
echo "  cookies:  $DATA_DIR/cookie.mjs"
if [ -f "$SKILL_SOURCE/SKILL.md" ]; then
  echo "  skill:    $SKILL_SOURCE/SKILL.md"
fi
if [ "${#SKILL_LINKS[@]}" -gt 0 ]; then
  for link in "${SKILL_LINKS[@]}"; do
    echo "  linked:   $link"
  done
fi
echo ""
echo "verify:    agenthail doctor"
echo ""
echo "Codex Desktop:"
echo "  agenthail launch codex       # launch with the local control bridge"
echo "Codex terminal:"
echo "  agenthail codex              # start a writable terminal session"
echo "  Plain codex sessions remain readable but read-only in Agenthail."
echo ""
echo "data dir:  ~/.agenthail/ (registry.db, daemon.pid, daemon.log)"

if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo ""
  echo "NOTE: $INSTALL_DIR is not on your PATH. Add it:"
  echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi
