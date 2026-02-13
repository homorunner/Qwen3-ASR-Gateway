#!/bin/bash
set -e

CONFIG_PATH="${1:-deploy/config.yaml}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOG_DIR="$PROJECT_ROOT/logs"

mkdir -p "$LOG_DIR"

echo "Building gateway..."
cd "$PROJECT_ROOT/gateway"

go build -o "$PROJECT_ROOT/bin/gateway" ./cmd/gateway/

echo "Gateway built: $PROJECT_ROOT/bin/gateway"
echo "Starting gateway with config: $CONFIG_PATH"
echo ""

cd "$PROJECT_ROOT"
exec ./bin/gateway --config "$CONFIG_PATH"
