#!/bin/bash
set -e

MODEL_PATH="${1:-Qwen/Qwen3-ASR-1.7B}"
GPU_MEM_UTIL="${2:-0.85}"
MAX_SESSIONS="${3:-64}"
BASE_PORT=50050
NUM_GPUS=8

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOG_DIR="$PROJECT_ROOT/logs"

mkdir -p "$LOG_DIR"

echo "Starting $NUM_GPUS engine workers..."
echo "  Model: $MODEL_PATH"
echo "  GPU memory utilization: $GPU_MEM_UTIL"
echo "  Max sessions per engine: $MAX_SESSIONS"
echo "  Ports: $BASE_PORT - $((BASE_PORT + NUM_GPUS - 1))"
echo ""

PIDS=()

for i in $(seq 0 $((NUM_GPUS - 1))); do
    PORT=$((BASE_PORT + i))
    LOG_FILE="$LOG_DIR/engine-${i}.log"

    echo "Starting engine $i on GPU $i, port $PORT (log: $LOG_FILE)"

    CUDA_VISIBLE_DEVICES=$i python -m engine \
        --model-path "$MODEL_PATH" \
        --port "$PORT" \
        --gpu-memory-utilization "$GPU_MEM_UTIL" \
        --max-sessions "$MAX_SESSIONS" \
        > "$LOG_FILE" 2>&1 &

    PIDS+=($!)
done

echo ""
echo "All engines started. PIDs: ${PIDS[*]}"
echo "Logs: $LOG_DIR/engine-*.log"
echo ""
echo "To stop all engines: kill ${PIDS[*]}"

# Save PIDs for later cleanup
echo "${PIDS[*]}" > "$LOG_DIR/engine_pids.txt"

# Wait for all processes
wait
