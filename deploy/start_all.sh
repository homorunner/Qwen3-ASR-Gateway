#!/bin/bash
set -e

MODEL_PATH="${1:-Qwen/Qwen3-ASR-1.7B}"
CONFIG_PATH="${2:-deploy/config.yaml}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOG_DIR="$PROJECT_ROOT/logs"

mkdir -p "$LOG_DIR"

echo "=========================================="
echo " Qwen3-ASR Gateway - Full Stack Startup"
echo "=========================================="
echo ""

# Start engines in background
echo "--- Starting Engine Workers ---"
bash "$SCRIPT_DIR/start_engines.sh" "$MODEL_PATH" &
ENGINE_LAUNCHER_PID=$!

# Wait for engines to load model (this takes a while)
echo ""
echo "Waiting 30s for engines to initialize..."
sleep 30

# Build and start gateway
echo ""
echo "--- Starting Gateway ---"
bash "$SCRIPT_DIR/start_gateway.sh" "$CONFIG_PATH" &
GATEWAY_PID=$!

echo ""
echo "=========================================="
echo " All services started"
echo "=========================================="
echo " Engine launcher PID: $ENGINE_LAUNCHER_PID"
echo " Gateway PID: $GATEWAY_PID"
echo ""
echo " WebSocket endpoint: ws://localhost:37901/ws/asr"
echo " Health check: http://localhost:37901/health"

wait
