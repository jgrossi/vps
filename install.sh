#!/usr/bin/env bash
# Usage: curl -sSL https://raw.githubusercontent.com/YOUR/vps/main/install.sh | bash
set -euo pipefail

REPO="jgrossi/vps"
ARCH=$(uname -m)
BIN="vps-linux-amd64"
[ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ] && BIN="vps-linux-arm64"

echo "→ Installing vps"
curl -sSL "https://github.com/$REPO/releases/download/latest/$BIN" -o /usr/local/bin/vps
chmod +x /usr/local/bin/vps
echo "✓ Done — copy apps.yml to /opt/vps/apps.yml then run: vps"
