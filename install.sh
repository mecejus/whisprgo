#!/usr/bin/env bash
set -eo pipefail

BINARY_DEST="/usr/local/bin/whisprgo"
PLIST_LABEL="com.whisprgo.agent"
PLIST_PATH="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"
CONFIG_PATH="$HOME/.config/whisprgo/config.json"
LOG_PATH="$HOME/.config/whisprgo/whisprgo.log"

# ── dependencies ──────────────────────────────────────────────────────────────

if ! command -v go &>/dev/null; then
    echo "Error: Go not found. Install with: brew install go"
    exit 1
fi

if ! pkg-config --exists portaudio-2.0 2>/dev/null; then
    echo "Error: portaudio not found. Install with: brew install portaudio"
    exit 1
fi

# ── build ─────────────────────────────────────────────────────────────────────

echo "→ Building whisprgo..."
go build -o whisprgo .

# ── api key ───────────────────────────────────────────────────────────────────

NEEDS_KEY=true
if [ -f "$CONFIG_PATH" ]; then
    if python3 -c "import json; d=json.load(open('$CONFIG_PATH')); exit(0 if d.get('api_key') else 1)" 2>/dev/null; then
        NEEDS_KEY=false
    fi
fi

if [ "$NEEDS_KEY" = true ]; then
    echo ""
    printf "Enter your Groq API key: "
    read -rs GROQ_KEY
    echo ""
    if [ -z "$GROQ_KEY" ]; then
        echo "Error: API key cannot be empty."
        exit 1
    fi
    mkdir -p "$(dirname "$CONFIG_PATH")"
    printf '{"api_key":"%s"}' "$GROQ_KEY" > "$CONFIG_PATH"
    chmod 600 "$CONFIG_PATH"
    echo "→ API key saved."
fi

# ── launch agent ──────────────────────────────────────────────────────────────

mkdir -p "$(dirname "$LOG_PATH")"
touch "$LOG_PATH"

# Stop any running service before replacing the binary. We can't trust
# `launchctl unload` alone — it may have been loaded in a different domain or
# the plist file may have changed shape — so we also pkill as a backstop and
# then poll until the process is actually gone.
echo "→ Stopping existing service..."
[ -f "$PLIST_PATH" ] && launchctl unload "$PLIST_PATH" 2>/dev/null || true
launchctl remove "$PLIST_LABEL" 2>/dev/null || true
pkill -f "$BINARY_DEST" 2>/dev/null || true

for _ in 1 2 3 4 5 6 7 8 9 10; do
    pgrep -f "$BINARY_DEST" >/dev/null 2>&1 || break
    sleep 0.3
done
if pgrep -f "$BINARY_DEST" >/dev/null 2>&1; then
    echo "  forcing kill..."
    pkill -9 -f "$BINARY_DEST" 2>/dev/null || true
    sleep 0.5
fi

# ── install binary ────────────────────────────────────────────────────────────

echo "→ Installing to $BINARY_DEST..."
sudo mkdir -p /usr/local/bin
sudo install -m 755 whisprgo "$BINARY_DEST"

echo "→ Writing LaunchAgent plist..."
cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${BINARY_DEST}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${LOG_PATH}</string>
    <key>StandardErrorPath</key>
    <string>${LOG_PATH}</string>
</dict>
</plist>
PLIST

echo "→ Starting service..."
launchctl load "$PLIST_PATH"

# ── verify ────────────────────────────────────────────────────────────────────
# `launchctl list` shows "<pid>\t<exit>\t<label>" for a healthy job and
# "-\t<exit>\t<label>" for one that crashed and is being respawned. A rebuild
# changes the binary's cdhash, which causes macOS TCC to revoke Accessibility
# for the new binary — the agent then fails its accessibility check on every
# launch and KeepAlive loops it forever. Detect that here so the user knows.

SERVICE_PID=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
    LINE=$(launchctl list 2>/dev/null | awk -v l="$PLIST_LABEL" '$3 == l {print; exit}')
    PID_FIELD=$(printf '%s' "$LINE" | awk '{print $1}')
    if [ -n "$PID_FIELD" ] && [ "$PID_FIELD" != "-" ]; then
        SERVICE_PID="$PID_FIELD"
        break
    fi
    sleep 0.3
done

# ── done ──────────────────────────────────────────────────────────────────────

echo ""
if [ -n "$SERVICE_PID" ]; then
    echo "✓ whisprgo is installed and running (pid $SERVICE_PID)."
else
    echo "✗ whisprgo installed, but the service did not stay running."
    echo ""
    echo "  Most likely Accessibility was revoked when the binary was rebuilt."
    echo "  Fix:"
    echo "    1. System Settings → Privacy & Security → Accessibility"
    echo "       Remove any old 'whisprgo' entry, then re-add: $BINARY_DEST"
    echo "    2. launchctl unload $PLIST_PATH"
    echo "       launchctl load   $PLIST_PATH"
    echo ""
    echo "  Recent log output:"
    tail -n 5 "$LOG_PATH" 2>/dev/null | sed 's/^/    /'
fi
echo ""
echo "  First-time setup — grant Accessibility access for the Fn-key hook:"
echo "  System Settings → Privacy & Security → Accessibility"
echo "  Click + and add: $BINARY_DEST"
echo ""
echo "  Logs:   tail -f $LOG_PATH"
echo "  Status: launchctl list | grep whisprgo"
echo "  Stop:   launchctl unload $PLIST_PATH"
