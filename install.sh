#!/bin/sh
set -e

REPO="mecejus/whisprgo"
BINARY="whisprgo"
INSTALL_DIR="/usr/local/bin"
PLIST_LABEL="com.whisprgo"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/$PLIST_LABEL.plist"
LOG_DIR="$HOME/Library/Logs/whisprgo"
LOG_FILE="$LOG_DIR/whisprgo.log"

if [ "$(uname)" != "Darwin" ]; then
  echo "Error: whisprgo requires macOS" >&2
  exit 1
fi

if [ "$(uname -m)" != "arm64" ]; then
  echo "Error: whisprgo requires Apple Silicon (M1 or later)" >&2
  exit 1
fi

echo "Fetching latest release..."
TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
  | grep -m1 '"tag_name"' | cut -d'"' -f4)

if [ -z "$TAG" ]; then
  echo "Error: could not determine latest release" >&2
  exit 1
fi

echo "Installing $BINARY $TAG..."

# Stop existing service before upgrading
if launchctl list 2>/dev/null | grep -q "$PLIST_LABEL"; then
  echo "Stopping existing service..."
  launchctl bootout "gui/$(id -u)/$PLIST_LABEL" 2>/dev/null || true
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "https://github.com/$REPO/releases/download/$TAG/$BINARY-darwin-arm64.tar.gz" \
  -o "$TMP/$BINARY.tar.gz"

tar xzf "$TMP/$BINARY.tar.gz" -C "$TMP"

sudo mkdir -p "$INSTALL_DIR"
sudo install -m 755 "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"

mkdir -p "$LOG_DIR"
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

launchctl bootstrap "gui/$(id -u)" "$PLIST_PATH"

echo ""
echo "whisprgo $TAG installed and service started."
echo ""
echo "On first launch a dialog will ask for your Groq API key"
echo "(https://console.groq.com), then macOS will request"
echo "Accessibility access. Grant it in System Settings, then restart:"
echo ""
echo "  launchctl kickstart -k \"gui/\$(id -u)/$PLIST_LABEL\""
echo ""
echo "Logs:  tail -f $LOG_FILE"
