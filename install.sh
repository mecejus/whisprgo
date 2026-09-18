#!/bin/sh
set -e

REPO="mecejus/whisprgo"
BINARY="whisprgo"
INSTALL_DIR="/usr/local/bin"
PLIST_LABEL="com.whisprgo"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/$PLIST_LABEL.plist"
CONFIG_DIR="$HOME/.config/whisprgo"
LOG_FILE="$CONFIG_DIR/whisprgo.log"

if [ "$(uname)" != "Darwin" ]; then
  echo "Error: whisprgo requires macOS" >&2
  exit 1
fi

if [ "$(uname -m)" != "arm64" ]; then
  echo "Error: whisprgo requires Apple Silicon (M1 or later)" >&2
  exit 1
fi

echo "Fetching latest release..."
TAG=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
  "https://github.com/$REPO/releases/latest" | sed 's|.*/||')

if [ -z "$TAG" ]; then
  echo "Error: could not determine latest release" >&2
  exit 1
fi

echo "Installing $BINARY $TAG..."

if launchctl list 2>/dev/null | grep -q "$PLIST_LABEL"; then
  echo "Stopping existing service..."
  launchctl bootout "gui/$(id -u)/$PLIST_LABEL" 2>/dev/null || true
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "https://github.com/$REPO/releases/download/$TAG/$BINARY-darwin-arm64" \
  -o "$TMP/$BINARY"

sudo mkdir -p "$INSTALL_DIR"
sudo install -m 755 "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"

mkdir -p "$CONFIG_DIR"
mkdir -p "$PLIST_DIR"

cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/${BINARY}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${LOG_FILE}</string>
    <key>StandardErrorPath</key>
    <string>${LOG_FILE}</string>
</dict>
</plist>
PLIST

# The service appends to the log, so remember where this launch's output
# starts: older runs have already printed the ready line.
LOG_START=0
if [ -f "$LOG_FILE" ]; then
  LOG_START=$(wc -l < "$LOG_FILE" | tr -d ' ')
fi

launchctl bootstrap "gui/$(id -u)" "$PLIST_PATH"

echo ""
echo "whisprgo $TAG installed and started."
echo ""
echo "Two dialogs will appear on first launch:"
echo ""
echo "  1. Paste your Groq API key (free at https://console.groq.com)."
echo "  2. Accessibility: click \"Open System Settings\" and turn whisprgo on."
echo "     Already listed and on? Turn it off and on again."
echo ""
echo "No restart needed: whisprgo starts by itself once access is granted."
echo "Waiting for it... (Ctrl-C stops waiting; the service keeps running.)"

# Poll this launch's log lines for the ready marker. The API key dialog is
# the slow part, so allow ten minutes before giving up on the wait itself.
waited=0
while [ "$waited" -lt 600 ]; do
  if [ -f "$LOG_FILE" ] && \
     tail -n +"$((LOG_START + 1))" "$LOG_FILE" | grep -q "whisprgo ready"; then
    echo ""
    echo "whisprgo is ready. Hold [fn] and speak; release to paste."
    exit 0
  fi
  sleep 2
  waited=$((waited + 2))
done

echo ""
echo "Still waiting for access. The service keeps trying; once whisprgo is"
echo "turned on under System Settings > Privacy & Security > Accessibility"
echo "it starts on its own."
echo ""
echo "Logs:  tail -f $LOG_FILE"
