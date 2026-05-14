#!/bin/sh
set -e

ASSET="whisprgo-darwin-arm64"
TAG="rolling-release"

go build -ldflags="-s -w" -o "$ASSET" .
trap 'rm -f "$ASSET"' EXIT

gh release upload "$TAG" "$ASSET" --clobber

echo "Uploaded $ASSET to $TAG."
