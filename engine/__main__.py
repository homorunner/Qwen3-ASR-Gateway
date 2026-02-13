"""
Engine Worker entry point.

Usage:
    CUDA_VISIBLE_DEVICES=0 python -m engine \
        --model-path Qwen/Qwen3-ASR-1.7B \
        --port 50050 \
        --gpu-memory-utilization 0.85 \
        --max-sessions 64
"""

import argparse
import asyncio
import logging
import os
import sys

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
logger = logging.getLogger("engine")


def parse_args():
    parser = argparse.ArgumentParser(description="Qwen3-ASR Engine Worker (gRPC)")

    parser.add_argument(
        "--model-path",
        default="Qwen/Qwen3-ASR-1.7B",
        help="Model name or local path (default: Qwen/Qwen3-ASR-1.7B)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=50050,
        help="gRPC listen port (default: 50050)",
    )
    parser.add_argument(
        "--gpu-memory-utilization",
        type=float,
        default=0.85,
        help="vLLM GPU memory utilization (default: 0.85)",
    )
    parser.add_argument(
        "--max-sessions",
        type=int,
        default=64,
        help="Maximum concurrent sessions (default: 64)",
    )
    parser.add_argument(
        "--max-concurrent-infer",
        type=int,
        default=16,
        help="Max concurrent inference threads (default: 16)",
    )
    parser.add_argument(
        "--max-new-tokens",
        type=int,
        default=32,
        help="Max new tokens per streaming step (default: 32)",
    )

    return parser.parse_args()


def main():
    args = parse_args()

    logger.info("Loading model: %s", args.model_path)
    logger.info("GPU memory utilization: %.2f", args.gpu_memory_utilization)

    from qwen_asr import Qwen3ASRModel

    asr_model = Qwen3ASRModel.LLM(
        model=args.model_path,
        gpu_memory_utilization=args.gpu_memory_utilization,
        max_new_tokens=args.max_new_tokens,
    )
    logger.info("Model loaded successfully.")

    from engine.server import serve

    asyncio.run(
        serve(
            asr_model=asr_model,
            port=args.port,
            max_sessions=args.max_sessions,
            max_concurrent_infer=args.max_concurrent_infer,
        )
    )


if __name__ == "__main__":
    main()
