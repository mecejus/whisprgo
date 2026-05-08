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

# ── install binary ────────────────────────────────────────────────────────────

echo "→ Installing to $BINARY_DEST..."
sudo mkdir -p /usr/local/bin
sudo install -m 755 whisprgo "$BINARY_DEST"

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

# Unload first if already installed (handles re-runs / upgrades)
if launchctl list 2>/dev/null | grep -q "$PLIST_LABEL"; then
    echo "→ Reloading existing service..."
    launchctl unload "$PLIST_PATH" 2>/dev/null || true
fi

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

# ── done ──────────────────────────────────────────────────────────────────────

echo ""
echo "✓ whisprgo is installed and running."
echo ""
echo "  IMPORTANT — grant Accessibility access for the Fn-key hook:"
echo "  System Settings → Privacy & Security → Accessibility"
echo "  Click + and add: $BINARY_DEST"
echo ""
echo "  Logs:   tail -f $LOG_PATH"
echo "  Status: launchctl list | grep whisprgo"
echo "  Stop:   launchctl unload $PLIST_PATH"
