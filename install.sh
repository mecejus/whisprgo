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

# macOS ties an Accessibility grant to the binary's code signature. The Go
# toolchain signs ad hoc, which binds the grant to one build's hash, so every
# upgrade used to need the grant removed and re-added by hand. Signing with a
# certificate binds the grant to the certificate instead. The certificate is
# created once on this Mac, in its own keychain, and every install signs the
# downloaded binary with it, so upgrades keep the grant.
SIGN_IDENTITY="whisprgo-local"
SIGN_IDENTIFIER="com.whisprgo"
KEYCHAIN="$HOME/Library/Keychains/whisprgo-signing.keychain-db"
KEYCHAIN_PW_FILE="$CONFIG_DIR/signing-keychain-password"

if [ "$(uname)" != "Darwin" ]; then
  echo "Error: whisprgo requires macOS" >&2
  exit 1
fi

if [ "$(uname -m)" != "arm64" ]; then
  echo "Error: whisprgo requires Apple Silicon (M1 or later)" >&2
  exit 1
fi

have_identity() {
  [ -f "$KEYCHAIN" ] && [ -f "$KEYCHAIN_PW_FILE" ] &&
    security find-certificate -c "$SIGN_IDENTITY" "$KEYCHAIN" >/dev/null 2>&1
}

create_identity() {
  echo "Creating a local code-signing certificate (first install only)..."
  work=$(mktemp -d)
  cat > "$work/openssl.cnf" <<CNF
[req]
distinguished_name = dn
prompt = no
[dn]
CN = $SIGN_IDENTITY
[ext]
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
basicConstraints = critical, CA:false
CNF
  # Apple's own openssl (LibreSSL), by full path: a Homebrew OpenSSL 3 on
  # PATH writes PKCS12 files that `security import` rejects ("MAC
  # verification failed"). The legacy algorithms are spelled out for the
  # same reason.
  /usr/bin/openssl req -x509 -newkey rsa:2048 -nodes -days 7300 \
    -config "$work/openssl.cnf" -extensions ext \
    -keyout "$work/key.pem" -out "$work/cert.pem" 2>/dev/null
  /usr/bin/openssl pkcs12 -export -inkey "$work/key.pem" -in "$work/cert.pem" \
    -keypbe PBE-SHA1-3DES -certpbe PBE-SHA1-3DES -macalg sha1 \
    -out "$work/identity.p12" -passout pass:whisprgo -name "$SIGN_IDENTITY"

  pw=$(/usr/bin/openssl rand -hex 24)
  mkdir -p "$CONFIG_DIR"
  (umask 077 && printf '%s' "$pw" > "$KEYCHAIN_PW_FILE")

  if [ -f "$KEYCHAIN" ]; then
    security delete-keychain "$KEYCHAIN" >/dev/null 2>&1 || true
  fi
  security create-keychain -p "$pw" "$KEYCHAIN"
  # No auto-lock; the private key is only ever used by codesign on this Mac.
  security set-keychain-settings "$KEYCHAIN"
  security import "$work/identity.p12" -k "$KEYCHAIN" -P whisprgo \
    -T /usr/bin/codesign >/dev/null
  # Without this, codesign pops a keychain password dialog on every use.
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s \
    -k "$pw" "$KEYCHAIN" >/dev/null
  # codesign only searches keychains on the user's search list.
  if ! security list-keychains -d user | grep -q "whisprgo-signing"; then
    # shellcheck disable=SC2046
    security list-keychains -d user -s "$KEYCHAIN" \
      $(security list-keychains -d user | tr -d '" ')
  fi
  rm -rf "$work"
}

sign_binary() {
  have_identity || create_identity
  security unlock-keychain -p "$(cat "$KEYCHAIN_PW_FILE")" "$KEYCHAIN"
  codesign --force --sign "$SIGN_IDENTITY" --identifier "$SIGN_IDENTIFIER" \
    --keychain "$KEYCHAIN" "$1"
}

# `install.sh sign <binary>` runs only the signing step. CI uses it to prove
# the recipe above on a real macOS runner against the binary it just built.
if [ "$1" = "sign" ]; then
  sign_binary "$2"
  exit 0
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

# An installed binary that is not signed with our certificate (a build from
# before signing, or ad hoc) holds an Accessibility grant bound to its hash.
# That grant cannot carry over, so it needs one more manual grant.
REGRANT=0
if [ -f "$INSTALL_DIR/$BINARY" ] && \
   ! codesign -dv "$INSTALL_DIR/$BINARY" 2>&1 | grep -q "Authority=$SIGN_IDENTITY"; then
  REGRANT=1
fi

sign_binary "$TMP/$BINARY"

sudo mkdir -p "$INSTALL_DIR"
sudo install -m 755 "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"

if [ "$REGRANT" = 1 ]; then
  # Drop the stale row so the prompt creates a fresh one for the signed
  # binary. Best effort: tccutil may not accept a path for a non-app binary.
  tccutil reset Accessibility "$INSTALL_DIR/$BINARY" >/dev/null 2>&1 || true
fi

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

# A leftover marker from an earlier wait would suppress the Accessibility
# prompt on this launch.
rm -f "$CONFIG_DIR/access-prompted"

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
if [ "$REGRANT" = 1 ]; then
  echo "This version is signed, so future upgrades keep their Accessibility"
  echo "grant. Getting there needs one more grant now:"
  echo ""
  echo "  Accessibility: click \"Open System Settings\". If whisprgo is still"
  echo "  listed, remove it with the minus button; then turn the new entry on."
else
  echo "Two dialogs will appear on first launch:"
  echo ""
  echo "  1. Paste your Groq API key (free at https://console.groq.com)."
  echo "  2. Accessibility: click \"Open System Settings\" and turn whisprgo on."
fi
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
