#!/bin/sh
set -e

# Removes everything install.sh created: the service, the binary, the
# settings folder (API key, log, markers), and the local code-signing
# keychain. After this the Mac is as it was before whisprgo was installed,
# except for the Accessibility entry, which macOS lets only the user remove.

BINARY="whisprgo"
INSTALL_DIR="/usr/local/bin"
PLIST_LABEL="com.whisprgo"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/$PLIST_LABEL.plist"
CONFIG_DIR="$HOME/.config/whisprgo"
KEYCHAIN="$HOME/Library/Keychains/whisprgo-signing.keychain-db"

echo ""
echo "Removing whisprgo..."

if launchctl list 2>/dev/null | grep -q "$PLIST_LABEL"; then
  launchctl bootout "gui/$(id -u)" "$PLIST_PATH" 2>/dev/null || \
    launchctl bootout "gui/$(id -u)/$PLIST_LABEL" 2>/dev/null || true
fi

rm -f "$PLIST_PATH"

if [ -f "$INSTALL_DIR/$BINARY" ]; then
  if ! sudo -n true 2>/dev/null; then
    echo "Your Mac will now ask for your password. That is to remove whisprgo from $INSTALL_DIR."
  fi
  sudo rm -f "$INSTALL_DIR/$BINARY"
fi

# Settings folder: API key, log, the Accessibility-prompt marker and the
# signing keychain's password.
rm -rf "$CONFIG_DIR"

# Log folder used by older versions.
rm -rf "$HOME/Library/Logs/whisprgo"

# The local code-signing certificate. delete-keychain also takes it off the
# keychain search list.
if [ -f "$KEYCHAIN" ]; then
  security delete-keychain "$KEYCHAIN" >/dev/null 2>&1 || rm -f "$KEYCHAIN"
fi

echo ""
echo "whisprgo is gone. Your API key and settings were removed too."
echo ""
echo "One thing macOS does not let a script do: whisprgo may still be listed under"
echo "System Settings > Privacy & Security > Accessibility. Select it and press"
echo "the minus (-) button to remove it. Leaving it there is harmless."
