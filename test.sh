#!/usr/bin/env bash
# Run the full integration test in a fresh Ubuntu 24.04 container.
# Usage: ./test.sh
set -euo pipefail

echo ""
echo "==> Building binary (linux/amd64)"
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o vps-linux-amd64 .

echo ""
echo "==> Building test image"
docker build -t vps-test -f test/Dockerfile .

echo ""
echo "==> Running integration test"
docker run --rm --privileged vps-test

echo ""
echo "==> Done"
