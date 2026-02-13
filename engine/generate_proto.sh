#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

python -m grpc_tools.protoc \
    -I"$PROJECT_ROOT/proto" \
    --python_out="$SCRIPT_DIR/proto/qwen_asr" \
    --grpc_python_out="$SCRIPT_DIR/proto/qwen_asr" \
    "$PROJECT_ROOT/proto/asr_engine.proto"

# Fix import path in generated gRPC file
sed -i.bak 's/import asr_engine_pb2/from . import asr_engine_pb2/' \
    "$SCRIPT_DIR/proto/qwen_asr/asr_engine_pb2_grpc.py"
rm -f "$SCRIPT_DIR/proto/qwen_asr/asr_engine_pb2_grpc.py.bak"

echo "Proto generated at: $SCRIPT_DIR/proto/qwen_asr/"
