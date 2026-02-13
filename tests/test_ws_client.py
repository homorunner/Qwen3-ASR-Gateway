"""
Usage:
    python tests/test_ws_client.py --gateway ws://localhost:37901/ws/asr --audio audio.wav
"""

import argparse
import asyncio
import io
import json
import struct
import sys
import time
import urllib.request
from typing import Tuple
import numpy as np
import websockets
import soundfile as sf


def read_wav(path: str) -> Tuple[np.ndarray, int]:
    wav, sr = sf.read(path, dtype="float32", always_2d=False)
    return np.asarray(wav, dtype=np.float32), int(sr)


def resample_to_16k(wav: np.ndarray, sr: int) -> np.ndarray:
    if sr == 16000:
        return wav.astype(np.float32, copy=False)
    wav = wav.astype(np.float32, copy=False)
    duration = wav.shape[0] / float(sr)
    n16k = int(round(duration * 16000))
    if n16k <= 0:
        return np.zeros((0,), dtype=np.float32)
    x_old = np.linspace(0.0, duration, num=wav.shape[0], endpoint=False)
    x_new = np.linspace(0.0, duration, num=n16k, endpoint=False)
    return np.interp(x_new, x_old, wav).astype(np.float32)


def pcm_to_bytes(pcm: np.ndarray) -> bytes:
    return pcm.astype(np.float32).tobytes()


async def stream_audio(
    gateway_url: str,
    wav16k: np.ndarray,
    chunk_ms: int = 500,
    language: str = "",
    context: str = "",
):
    sr = 16000
    chunk_samples = int(round(chunk_ms / 1000.0 * sr))

    print(f"\nConnecting to {gateway_url} ...")
    async with websockets.connect(gateway_url) as ws:
        # 1. Send start message
        start_msg = {
            "type": "start",
            "config": {
                "language": language,
                "context": context,
                "sample_rate": 16000,
                "encoding": "pcm_f32le",
                "chunk_size_sec": 2.0,
                "unfixed_chunk_num": 2,
                "unfixed_token_num": 5,
            },
        }
        await ws.send(json.dumps(start_msg))
        print("Sent 'start' message")

        resp = json.loads(await ws.recv())
        session_id = resp.get("session_id", "")
        print(f"Session started: {session_id}\n")

        pos = 0
        chunk_idx = 0
        total_samples = wav16k.shape[0]
        duration_sec = total_samples / float(sr)
        start_time = time.time()

        while pos < total_samples:
            seg = wav16k[pos : pos + chunk_samples]
            pos += seg.shape[0]
            chunk_idx += 1

            await ws.send(pcm_to_bytes(seg))

            resp = json.loads(await ws.recv())
            elapsed = time.time() - start_time
            audio_pos = pos / float(sr)

            if resp.get("type") == "result":
                lang = resp.get("language", "")
                text = resp.get("text", "")
                print(
                    f"[chunk {chunk_idx:03d}] "
                    f"audio={audio_pos:.1f}s/{duration_sec:.1f}s "
                    f"elapsed={elapsed:.1f}s "
                    f"lang={lang!r} "
                    f"text={text!r}"
                )
            elif resp.get("type") == "error":
                print(f"[ERROR] {resp.get('code')}: {resp.get('message')}")
                return

        await ws.send(json.dumps({"type": "finish"}))
        print("\nSent 'finish' message")

        resp = json.loads(await ws.recv())
        if resp.get("type") == "result" and resp.get("is_final"):
            print(f"\n{'='*60}")
            print(f"FINAL RESULT:")
            print(f"  Language: {resp.get('language', '')}")
            print(f"  Text: {resp.get('text', '')}")
            print(f"{'='*60}")
        else:
            print(f"Final response: {resp}")

        total_elapsed = time.time() - start_time
        rtf = total_elapsed / duration_sec if duration_sec > 0 else 0
        print(f"\nTotal time: {total_elapsed:.1f}s  Audio: {duration_sec:.1f}s  RTF: {rtf:.2f}")


def main():
    parser = argparse.ArgumentParser(description="Qwen3-ASR Gateway WebSocket Test Client")
    parser.add_argument(
        "--gateway",
        default="ws://localhost:37901/ws/asr",
        help="Gateway WebSocket URL (default: ws://localhost:37901/ws/asr)",
    )
    parser.add_argument(
        "--audio",
        default=None,
        help="Path to WAV audio file",
    )
    parser.add_argument(
        "--chunk-ms",
        type=int,
        default=500,
        help="Audio chunk size in milliseconds (default: 500)",
    )
    parser.add_argument(
        "--language",
        default="",
        help="Force language (e.g. 'Chinese', 'English'). Empty = auto detect.",
    )
    parser.add_argument(
        "--context",
        default="",
        help="Context hint for ASR",
    )
    args = parser.parse_args()

    wav, sr = read_wav(args.audio)
    print(f"Audio: {wav.shape[0]} samples, {sr} Hz, {wav.shape[0]/sr:.1f}s")
    wav16k = resample_to_16k(wav, sr)
    print(f"Resampled to 16kHz: {wav16k.shape[0]} samples, {wav16k.shape[0]/16000:.1f}s")

    asyncio.run(
        stream_audio(
            gateway_url=args.gateway,
            wav16k=wav16k,
            chunk_ms=args.chunk_ms,
            language=args.language,
            context=args.context,
        )
    )


if __name__ == "__main__":
    main()
