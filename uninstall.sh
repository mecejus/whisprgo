#!/bin/sh
set -e

BINARY="whisprgo"
INSTALL_DIR="/usr/local/bin"
PLIST_LABEL="com.whisprgo"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/$PLIST_LABEL.plist"
CONFIG_DIR="$HOME/.config/whisprgo"

if launchctl list 2>/dev/null | grep -q "$PLIST_LABEL"; then
  echo "Stopping service..."
  launchctl bootout "gui/$(id -u)" "$PLIST_PATH" 2>/dev/null || \
    launchctl bootout "gui/$(id -u)/$PLIST_LABEL" 2>/dev/null || true
fi

if [ -f "$PLIST_PATH" ]; then
  rm "$PLIST_PATH"
  echo "Removed LaunchAgent."
fi

if [ -f "$INSTALL_DIR/$BINARY" ]; then
  sudo rm "$INSTALL_DIR/$BINARY"
  echo "Removed $INSTALL_DIR/$BINARY."
fi

# Clean up legacy log dir from older installs.
rm -rf "$HOME/Library/Logs/whisprgo"

if [ -d "$CONFIG_DIR" ]; then
  printf "Remove API key and logs at %s? [y/N] " "$CONFIG_DIR"
  read -r confirm
  case "$confirm" in
    y|Y) rm -rf "$CONFIG_DIR"; echo "Removed $CONFIG_DIR." ;;
    *)   echo "Kept $CONFIG_DIR." ;;
  esac
fi

echo ""
echo "whisprgo uninstalled."
