"""Latest-frame-only OpenCV source with reconnect handling."""

from __future__ import annotations

import logging
import os
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

import cv2
import numpy as np
import numpy.typing as npt

LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class CapturedFrame:
    image: npt.NDArray[np.uint8]
    timestamp: datetime
    generation: int


class FrameSource:
    """Read in a dedicated thread and retain only the newest unread frame."""

    def __init__(self, camera: int, url: str):
        self.camera = camera
        self._url = url[5:] if url.startswith("file:") else url
        self._is_file = url.startswith("file:") or not url.lower().startswith(("rtsp://", "rtsps://"))
        self._condition = threading.Condition()
        self._latest: CapturedFrame | None = None
        self._consumed_generation = 0
        self._generation = 0
        self._state = "starting"
        self._dropped = 0
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    @property
    def state(self) -> str:
        with self._condition:
            return self._state

    @property
    def dropped(self) -> int:
        with self._condition:
            return self._dropped

    def start(self) -> None:
        if self._thread is not None:
            return
        self._thread = threading.Thread(
            target=self._run, name=f"camera-{self.camera}", daemon=True
        )
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()
        with self._condition:
            self._condition.notify_all()
        if self._thread is not None:
            self._thread.join(timeout=5)

    def get_latest(self, timeout: float = 0.25) -> CapturedFrame | None:
        deadline = time.monotonic() + timeout
        with self._condition:
            while (
                not self._stop.is_set()
                and (self._latest is None or self._latest.generation <= self._consumed_generation)
            ):
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    return None
                self._condition.wait(remaining)
            if self._latest is None or self._latest.generation <= self._consumed_generation:
                return None
            self._consumed_generation = self._latest.generation
            return self._latest

    def _set_state(self, state: str) -> None:
        with self._condition:
            self._state = state
            self._condition.notify_all()

    def _open(self) -> cv2.VideoCapture:
        if not self._is_file:
            os.environ.setdefault("OPENCV_FFMPEG_CAPTURE_OPTIONS", "rtsp_transport;tcp")
            return cv2.VideoCapture(self._url, cv2.CAP_FFMPEG)
        return cv2.VideoCapture(str(Path(self._url)))

    def _run(self) -> None:
        backoff = 1.0
        while not self._stop.is_set():
            capture = self._open()
            if not capture.isOpened():
                capture.release()
                self._set_state("reconnecting")
                LOGGER.warning("camera %s unavailable; retrying", self.camera)
                if self._stop.wait(backoff):
                    break
                backoff = min(backoff * 2, 30.0)
                continue
            fps = capture.get(cv2.CAP_PROP_FPS) if self._is_file else 0.0
            interval = 1.0 / fps if fps and fps > 0 else 0.0
            next_frame_at = time.monotonic()
            backoff = 1.0
            self._set_state("ok")
            LOGGER.info("camera %s connected", self.camera)
            while not self._stop.is_set():
                ok, frame = capture.read()
                if not ok:
                    break
                if interval:
                    delay = next_frame_at - time.monotonic()
                    if delay > 0 and self._stop.wait(delay):
                        break
                    next_frame_at = max(next_frame_at + interval, time.monotonic())
                captured_at = datetime.now(timezone.utc)
                with self._condition:
                    self._generation += 1
                    if self._latest is not None and self._latest.generation > self._consumed_generation:
                        self._dropped += 1
                    self._latest = CapturedFrame(frame, captured_at, self._generation)
                    self._condition.notify_all()
            capture.release()
            if self._stop.is_set():
                break
            self._set_state("reconnecting")
            LOGGER.warning("camera %s disconnected; reconnecting", self.camera)
            if self._stop.wait(backoff):
                break
            backoff = min(backoff * 2, 30.0)

