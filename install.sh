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
  printf '\r\033[KSetting up code signing (first time only)...'
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

# Progress helpers. Every step prints one line; anything that takes more
# than a moment shows a spinner with elapsed seconds so the person can see
# the installer is alive.
step() { printf '%s\n' "$1"; }
done_line() { printf '\r\033[K%s done\n' "$1"; }

echo ""
step "Checking for the latest version..."
TAG=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
  "https://github.com/$REPO/releases/latest" | sed 's|.*/||')

if [ -z "$TAG" ]; then
  echo "Could not reach GitHub to download whisprgo. Check your internet connection and try again." >&2
  exit 1
fi

if launchctl list 2>/dev/null | grep -q "$PLIST_LABEL"; then
  launchctl bootout "gui/$(id -u)/$PLIST_LABEL" 2>/dev/null || true
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

step "Downloading whisprgo $TAG..."
curl -fL# "https://github.com/$REPO/releases/download/$TAG/$BINARY-darwin-arm64" \
  -o "$TMP/$BINARY"

# An installed binary that is not signed with our certificate (a build from
# before signing, or ad hoc) holds an Accessibility grant bound to its hash.
# That grant cannot carry over, so it needs one more manual grant.
REGRANT=0
if [ -f "$INSTALL_DIR/$BINARY" ] && \
   ! codesign -dv "$INSTALL_DIR/$BINARY" 2>&1 | grep -q "Authority=$SIGN_IDENTITY"; then
  REGRANT=1
fi

# A fresh install has no API key yet, so the first launch asks for one.
FIRST_RUN=0
if [ ! -f "$CONFIG_DIR/config.json" ]; then
  FIRST_RUN=1
fi

printf 'Signing...'
sign_binary "$TMP/$BINARY"
done_line "Signing..."

if ! sudo -n true 2>/dev/null; then
  echo "Your Mac will now ask for your password. That is to place whisprgo in $INSTALL_DIR."
fi
sudo mkdir -p "$INSTALL_DIR"
sudo install -m 755 "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
step "Installed to $INSTALL_DIR/$BINARY."

# There is no scripted way to drop the stale Accessibility row: tccutil only
# takes bundle identifiers, and the binary has none. The user removes it.

mkdir -p "$CONFIG_DIR"
mkdir -p "$PLIST_DIR"

# ThrottleInterval: while waiting for the Accessibility grant the service
# exits and restarts on purpose (see main.go); launchd's default 10-second
# restart throttle would make the grant take up to ten seconds to notice.
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
    <key>ThrottleInterval</key>
    <integer>1</integer>
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
if [ "$FIRST_RUN" = 1 ]; then
  echo "Almost done. Two things will pop up on your screen:"
  echo ""
  echo "  1. A box asking for your Groq API key."
  echo "     Get a free one at https://console.groq.com, paste it in, click OK."
  echo ""
  echo "  2. A macOS message about Accessibility."
  echo "     Click \"Open System Settings\" and switch whisprgo on."
  echo ""
  WAIT_MSG="Waiting for you to do those (leave this window open)"
elif [ "$REGRANT" = 1 ]; then
  echo "Almost done. macOS will ask for Accessibility access one more time."
  echo "From this version on it stays granted across updates."
  echo ""
  echo "  Click \"Open System Settings\". If whisprgo is already in the list,"
  echo "  select it and press the minus (-) button. Then switch whisprgo on."
  echo ""
  WAIT_MSG="Waiting for you to do that (leave this window open)"
else
  WAIT_MSG="Starting whisprgo"
fi

# Poll this launch's log lines for the ready marker, four times a second,
# behind a spinner. The API key dialog is the slow part, so allow ten
# minutes before giving up on the wait itself.
ticks=0
while [ "$ticks" -lt 2400 ]; do
  if [ -f "$LOG_FILE" ] && \
     tail -n +"$((LOG_START + 1))" "$LOG_FILE" | grep -q "whisprgo ready"; then
    printf '\r\033[K'
    echo "All set. Hold the fn key, talk, let go. Your words appear where you were typing."
    exit 0
  fi
  case $((ticks % 4)) in
    0) c='|' ;; 1) c='/' ;; 2) c='-' ;; 3) c='\' ;;
  esac
  printf '\r\033[K%s %s... %ss ' "$c" "$WAIT_MSG" "$((ticks / 4))"
  sleep 0.25
  ticks=$((ticks + 1))
done

printf '\r\033[K'
echo "Still waiting, and that is fine. whisprgo keeps checking in the background"
echo "and starts by itself as soon as you switch it on in System Settings."
echo "You can close this window."
