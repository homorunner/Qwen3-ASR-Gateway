import asyncio
import logging
import threading
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from typing import Dict, Optional

import grpc
import numpy as np
from grpc import aio as grpc_aio

from engine.proto.qwen_asr import asr_engine_pb2 as pb2
from engine.proto.qwen_asr import asr_engine_pb2_grpc as pb2_grpc

logger = logging.getLogger(__name__)


@dataclass
class SessionEntry:
    """Wrapper around ASRStreamingState with session-level metadata."""
    streaming_state: object  # ASRStreamingState
    config: pb2.SessionConfig
    sample_rate: int = 16000
    encoding: str = "pcm_f32le"


def decode_audio(raw_bytes: bytes, encoding: str, sample_rate: int) -> np.ndarray:
    """Decode raw PCM bytes to float32 numpy array at 16kHz."""
    if encoding == "pcm_f32le":
        pcm = np.frombuffer(raw_bytes, dtype=np.float32).copy()
    elif encoding == "pcm_s16le":
        pcm = np.frombuffer(raw_bytes, dtype=np.int16).astype(np.float32) / 32768.0
    else:
        raise ValueError(f"Unsupported encoding: {encoding}")

    # Resample to 16kHz if needed
    if sample_rate != 16000:
        try:
            import librosa
            pcm = librosa.resample(pcm, orig_sr=sample_rate, target_sr=16000)
        except ImportError:
            # Fallback: simple linear interpolation
            duration = len(pcm) / float(sample_rate)
            n16k = int(round(duration * 16000))
            if n16k <= 0:
                return np.zeros((0,), dtype=np.float32)
            x_old = np.linspace(0.0, duration, num=len(pcm), endpoint=False)
            x_new = np.linspace(0.0, duration, num=n16k, endpoint=False)
            pcm = np.interp(x_new, x_old, pcm).astype(np.float32)

    return pcm


class ASREngineServicer(pb2_grpc.ASREngineServicer):
    """gRPC service implementation for ASR Engine."""

    def __init__(
        self,
        asr_model,
        max_sessions: int = 64,
        max_concurrent_infer: int = 16,
    ):
        self.asr = asr_model
        self.max_sessions = max_sessions

        self.sessions: Dict[str, SessionEntry] = {}
        self.lock = threading.Lock()

        self.executor = ThreadPoolExecutor(
            max_workers=max_concurrent_infer,
            thread_name_prefix="asr-infer",
        )

        self._model_loaded = True
        logger.info(
            "ASREngineServicer initialized: max_sessions=%d, max_concurrent_infer=%d",
            max_sessions, max_concurrent_infer,
        )

    async def CreateSession(self, request, context):
        """Create a new streaming session."""
        session_id = request.session_id
        config = request.config

        with self.lock:
            if session_id in self.sessions:
                return pb2.CreateSessionResponse(
                    success=False,
                    error_message=f"Session {session_id} already exists",
                )
            if len(self.sessions) >= self.max_sessions:
                return pb2.CreateSessionResponse(
                    success=False,
                    error_message=f"Max sessions ({self.max_sessions}) reached",
                )

        try:
            language = config.language if config.language else None
            context_str = config.context if config.context else ""
            chunk_size_sec = config.chunk_size_sec if config.chunk_size_sec > 0 else 2.0
            unfixed_chunk_num = config.unfixed_chunk_num if config.unfixed_chunk_num > 0 else 2
            unfixed_token_num = config.unfixed_token_num if config.unfixed_token_num > 0 else 5

            streaming_state = self.asr.init_streaming_state(
                context=context_str,
                language=language,
                unfixed_chunk_num=unfixed_chunk_num,
                unfixed_token_num=unfixed_token_num,
                chunk_size_sec=chunk_size_sec,
            )
        except Exception as e:
            logger.error("Failed to init streaming state for session %s: %s", session_id, e)
            return pb2.CreateSessionResponse(
                success=False,
                error_message=str(e),
            )

        sample_rate = config.sample_rate if config.sample_rate > 0 else 16000
        encoding = config.encoding if config.encoding else "pcm_f32le"

        entry = SessionEntry(
            streaming_state=streaming_state,
            config=config,
            sample_rate=sample_rate,
            encoding=encoding,
        )

        with self.lock:
            self.sessions[session_id] = entry

        logger.info(
            "Session created: %s (language=%s, sample_rate=%d, encoding=%s, chunk_size=%.1fs)",
            session_id, language or "auto", sample_rate, encoding, chunk_size_sec,
        )
        return pb2.CreateSessionResponse(success=True)

    async def PushAudio(self, request, context):
        """Push audio data and get transcription result."""
        session_id = request.session_id

        with self.lock:
            entry = self.sessions.get(session_id)

        if entry is None:
            context.set_code(grpc.StatusCode.NOT_FOUND)
            context.set_details(f"Session {session_id} not found")
            return pb2.PushAudioResponse()

        if not request.audio_data:
            return pb2.PushAudioResponse(
                language=entry.streaming_state.language,
                text=entry.streaming_state.text,
                updated=False,
            )

        try:
            pcm = decode_audio(request.audio_data, entry.encoding, entry.sample_rate)
        except Exception as e:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details(f"Audio decode error: {e}")
            return pb2.PushAudioResponse()

        old_text = entry.streaming_state.text

        loop = asyncio.get_event_loop()
        try:
            await loop.run_in_executor(
                self.executor,
                self.asr.streaming_transcribe,
                pcm,
                entry.streaming_state,
            )
        except Exception as e:
            logger.error("Inference error for session %s: %s", session_id, e)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Inference error: {e}")
            return pb2.PushAudioResponse()

        updated = entry.streaming_state.text != old_text

        return pb2.PushAudioResponse(
            language=entry.streaming_state.language or "",
            text=entry.streaming_state.text or "",
            updated=updated,
        )

    async def FinishSession(self, request, context):
        """Finish a session, flush remaining audio, return final result."""
        session_id = request.session_id

        with self.lock:
            entry = self.sessions.pop(session_id, None)

        if entry is None:
            context.set_code(grpc.StatusCode.NOT_FOUND)
            context.set_details(f"Session {session_id} not found")
            return pb2.FinishSessionResponse()

        loop = asyncio.get_event_loop()
        try:
            await loop.run_in_executor(
                self.executor,
                self.asr.finish_streaming_transcribe,
                entry.streaming_state,
            )
        except Exception as e:
            logger.error("Finish error for session %s: %s", session_id, e)
            pass

        logger.info("Session finished: %s", session_id)
        return pb2.FinishSessionResponse(
            language=entry.streaming_state.language or "",
            text=entry.streaming_state.text or "",
        )

    async def DestroySession(self, request, context):
        """Destroy a session without flushing (abnormal disconnect)."""
        session_id = request.session_id

        with self.lock:
            entry = self.sessions.pop(session_id, None)

        if entry is not None:
            logger.info("Session destroyed (without flush): %s", session_id)

        return pb2.DestroySessionResponse(success=True)

    async def HealthCheck(self, request, context):
        """Return engine health status."""
        with self.lock:
            active = len(self.sessions)

        # TODO: Add GPU memory usage reporting via pynvml or torch.cuda
        return pb2.HealthCheckResponse(
            active_sessions=active,
            max_sessions=self.max_sessions,
            model_loaded=self._model_loaded,
            gpu_memory_used_pct=0.0,
        )


async def serve(
    asr_model,
    port: int,
    max_sessions: int = 64,
    max_concurrent_infer: int = 16,
):
    """Start the gRPC async server."""
    server = grpc_aio.server(
        options=[
            ("grpc.max_receive_message_length", 64 * 1024 * 1024),
            ("grpc.max_send_message_length", 64 * 1024 * 1024),
        ],
    )

    servicer = ASREngineServicer(
        asr_model=asr_model,
        max_sessions=max_sessions,
        max_concurrent_infer=max_concurrent_infer,
    )
    pb2_grpc.add_ASREngineServicer_to_server(servicer, server)

    listen_addr = f"0.0.0.0:{port}"
    server.add_insecure_port(listen_addr)

    logger.info("Engine gRPC server starting on %s", listen_addr)
    await server.start()
    logger.info("Engine gRPC server started on %s", listen_addr)

    await server.wait_for_termination()
