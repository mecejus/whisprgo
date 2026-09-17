#!/bin/sh
# Releases are built on GitHub's macOS runners and published automatically on
# every push to main — see .github/workflows/build.yml. This just triggers
# that workflow by hand; nothing is compiled locally.
set -e

gh workflow run build.yml --ref main
echo "Triggered. Follow it with: gh run watch"
