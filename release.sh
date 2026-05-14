#!/bin/sh
set -e

ASSET="whisprgo-darwin-arm64"
TAG="rolling-release"

go build -ldflags="-s -w" -o "$ASSET" .
trap 'rm -f "$ASSET"' EXIT

COMMIT=$(git rev-parse HEAD)
SHORT=$(git rev-parse --short HEAD)

# Replace the rolling release so the source archive matches the binary.
gh release delete "$TAG" --yes --cleanup-tag 2>/dev/null || true

gh release create "$TAG" "$ASSET" \
  --title "Whispr Go" \
  --target "$COMMIT" \
  --notes "Built from commit \`$SHORT\`."

echo "Released $ASSET as $TAG ($SHORT)."
