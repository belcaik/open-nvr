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
        self._capture: cv2.VideoCapture | None = None

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
        self._stop = threading.Event()
        with self._condition:
            self._latest = None
            self._state = "starting"
            self._condition.notify_all()
        self._thread = threading.Thread(
            target=self._run,
            args=(self._stop,),
            name=f"camera-{self.camera}",
            daemon=True,
        )
        self._thread.start()

    def stop(self) -> None:
        if self._thread is None:
            return
        self._stop.set()
        with self._condition:
            self._condition.notify_all()
        # La captura solo la suelta su propio hilo: VideoCapture no admite
        # release() concurrente con open() o read(). Un hilo bloqueado en
        # open() se abandona; su evento ya está puesto y saldrá solo.
        self._thread.join(timeout=4)
        self._thread = None

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

    def _set_state(self, state: str, stop: threading.Event) -> None:
        with self._condition:
            if self._stop is not stop:
                return
            self._state = state
            self._condition.notify_all()

    def _release_capture(self, capture: cv2.VideoCapture) -> None:
        with self._condition:
            if self._capture is capture:
                self._capture = None
        capture.release()

    def _open(self, stop: threading.Event) -> cv2.VideoCapture:
        capture = cv2.VideoCapture()
        with self._condition:
            if self._stop is stop:
                self._capture = capture
        if not self._is_file:
            os.environ.setdefault("OPENCV_FFMPEG_CAPTURE_OPTIONS", "rtsp_transport;tcp")
            capture.open(self._url, cv2.CAP_FFMPEG)
        else:
            capture.open(str(Path(self._url)))
        return capture

    def _run(self, stop: threading.Event) -> None:
        backoff = 1.0
        while not stop.is_set():
            capture = self._open(stop)
            if stop.is_set():
                self._release_capture(capture)
                break
            if not capture.isOpened():
                self._release_capture(capture)
                self._set_state("reconnecting", stop)
                LOGGER.warning("camera %s unavailable; retrying", self.camera)
                if stop.wait(backoff):
                    break
                backoff = min(backoff * 2, 30.0)
                continue
            fps = capture.get(cv2.CAP_PROP_FPS) if self._is_file else 0.0
            interval = 1.0 / fps if fps and fps > 0 else 0.0
            next_frame_at = time.monotonic()
            backoff = 1.0
            self._set_state("ok", stop)
            LOGGER.info("camera %s connected", self.camera)
            while not stop.is_set():
                ok, frame = capture.read()
                if not ok:
                    break
                if interval:
                    delay = next_frame_at - time.monotonic()
                    if delay > 0 and stop.wait(delay):
                        break
                    next_frame_at = max(next_frame_at + interval, time.monotonic())
                captured_at = datetime.now(timezone.utc)
                with self._condition:
                    # Un hilo abandonado no publica frames de una fuente parada.
                    if self._stop is not stop:
                        break
                    self._generation += 1
                    if self._latest is not None and self._latest.generation > self._consumed_generation:
                        self._dropped += 1
                    self._latest = CapturedFrame(frame, captured_at, self._generation)
                    self._condition.notify_all()
            self._release_capture(capture)
            if stop.is_set():
                break
            self._set_state("reconnecting", stop)
            LOGGER.warning("camera %s disconnected; reconnecting", self.camera)
            if stop.wait(backoff):
                break
            backoff = min(backoff * 2, 30.0)

