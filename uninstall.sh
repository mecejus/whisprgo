#!/usr/bin/env bash
set -eo pipefail

BINARY_DEST="/usr/local/bin/whisprgo"
PLIST_LABEL="com.whisprgo.agent"
PLIST_PATH="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"

echo "→ Stopping service..."
launchctl unload "$PLIST_PATH" 2>/dev/null || true

echo "→ Removing LaunchAgent plist..."
rm -f "$PLIST_PATH"

echo "→ Removing binary..."
sudo rm -f "$BINARY_DEST"

echo ""
echo "✓ whisprgo uninstalled."
echo "  Config and API key kept at ~/.config/whisprgo — remove manually if no longer needed:"
echo "  rm -rf ~/.config/whisprgo"
