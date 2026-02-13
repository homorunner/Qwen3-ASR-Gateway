#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

mkdir -p "$SCRIPT_DIR/proto/qwen_asr"

protoc \
    -I"$PROJECT_ROOT/proto" \
    --go_out="$SCRIPT_DIR/proto/qwen_asr" \
    --go_opt=paths=source_relative \
    --go-grpc_out="$SCRIPT_DIR/proto/qwen_asr" \
    --go-grpc_opt=paths=source_relative \
    "$PROJECT_ROOT/proto/asr_engine.proto"

echo "Proto generated at: $SCRIPT_DIR/proto/qwen_asr/"
