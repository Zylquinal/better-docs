#!/usr/bin/env bash
set -euo pipefail

OS=$(uname | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *)
    echo "ERROR: unsupported architecture '$ARCH'"
    exit 1
    ;;
esac

BINARY="better-docs-server-${OS}-${ARCH}"
if [ ! -x "$BINARY" ]; then
  echo "ERROR: binary '$BINARY' not found or not executable"
  exit 1
fi

exec ./"$BINARY" "$@"
