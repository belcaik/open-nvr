"""WebSocket/HTTP server and per-camera detection loops."""

from __future__ import annotations

import asyncio
import json
import logging
import statistics
import time
from collections import deque
from datetime import timezone
from http import HTTPStatus
from typing import Any

from websockets.asyncio.server import ServerConnection, serve
from websockets.datastructures import Headers
from websockets.http11 import Request, Response

from .model import YoloDetector
from .source import FrameSource
from .tracker import PersonTracker

LOGGER = logging.getLogger(__name__)


class DetectionServer:
    def __init__(self, host: str, port: int, queue_size: int = 8):
        self.host = host
        self.port = port
        self.queue_size = queue_size
        self.statuses: dict[int, dict[str, Any]] = {}
        self._clients: set[asyncio.Queue[str | None]] = set()
        self._server: Any = None

    async def start(self) -> None:
        self._server = await serve(
            self._websocket,
            self.host,
            self.port,
            process_request=self._http_request,
            compression=None,
        )

    async def close(self) -> None:
        if self._server is not None:
            self._server.close()
            await self._server.wait_closed()
        for queue in tuple(self._clients):
            self._offer(queue, None)

    def publish(self, message: dict[str, Any]) -> None:
        encoded = json.dumps(message, separators=(",", ":"))
        for queue in tuple(self._clients):
            self._offer(queue, encoded)

    def publish_status(self, status: dict[str, Any]) -> None:
        self.statuses[int(status["cam"])] = status
        self.publish(status)

    @staticmethod
    def _offer(queue: asyncio.Queue[str | None], item: str | None) -> None:
        if queue.full():
            try:
                queue.get_nowait()
            except asyncio.QueueEmpty:
                pass
        try:
            queue.put_nowait(item)
        except asyncio.QueueFull:
            pass

    async def _websocket(self, connection: ServerConnection) -> None:
        if connection.request.path != "/detections":
            await connection.close(code=1008, reason="not found")
            return
        queue: asyncio.Queue[str | None] = asyncio.Queue(self.queue_size)
        self._clients.add(queue)
        try:
            while True:
                message = await queue.get()
                if message is None:
                    return
                await connection.send(message)
        except Exception:
            pass
        finally:
            self._clients.discard(queue)

    def _http_request(
        self, connection: ServerConnection, request: Request
    ) -> Response | None:
        if request.path != "/healthz":
            return None
        body = json.dumps(
            [self.statuses[camera] for camera in sorted(self.statuses)],
            separators=(",", ":"),
        ).encode()
        headers = Headers()
        headers["Content-Type"] = "application/json"
        headers["Content-Length"] = str(len(body))
        return Response(HTTPStatus.OK, "OK", headers, body)


class CameraWorker:
    def __init__(
        self,
        camera: int,
        source: FrameSource,
        detector: YoloDetector,
        tracker: PersonTracker,
        server: DetectionServer,
    ):
        self.camera = camera
        self.source = source
        self.detector = detector
        self.tracker = tracker
        self.server = server
        self.sequence = 0
        self._inference_ms: deque[float] = deque(maxlen=300)
        self._processed_at: deque[float] = deque(maxlen=300)
        self._last_state: str | None = None
        self._last_status = 0.0

    def _status(self, now: float) -> dict[str, Any]:
        cutoff = now - 5.0
        fps = sum(value >= cutoff for value in self._processed_at) / 5.0
        return {
            "type": "status",
            "cam": self.camera,
            "state": self.source.state,
            "fps": round(fps, 1),
            "infer_ms_p50": round(statistics.median(self._inference_ms), 1)
            if self._inference_ms
            else 0.0,
            "dropped": self.source.dropped,
        }

    def _infer(self, image: Any) -> tuple[list[Any], float]:
        started = time.perf_counter()
        boxes, confidences = self.detector.detect(image)
        inference_ms = (time.perf_counter() - started) * 1000.0
        return self.tracker.update(boxes, confidences), inference_ms

    async def run(self, stop: asyncio.Event) -> None:
        self.source.start()
        try:
            while not stop.is_set():
                now = time.monotonic()
                state = self.source.state
                if state != self._last_state or now - self._last_status >= 5.0:
                    self.server.publish_status(self._status(now))
                    self._last_state = state
                    self._last_status = now
                frame = self.source.get_latest(0)
                if frame is None:
                    await asyncio.sleep(0.01)
                    continue
                # En un hilo: ORT suelta el GIL, así cada cámara infiere en
                # paralelo y el bucle sigue atendiendo a los clientes.
                tracks, inference_ms = await asyncio.to_thread(self._infer, frame.image)
                self.sequence += 1
                processed = time.monotonic()
                self._inference_ms.append(inference_ms)
                self._processed_at.append(processed)
                height, width = frame.image.shape[:2]
                self.server.publish(
                    {
                        "type": "detections",
                        "cam": self.camera,
                        "seq": self.sequence,
                        "ts": frame.timestamp.astimezone(timezone.utc).isoformat(
                            timespec="microseconds"
                        ).replace("+00:00", "Z"),
                        "w": width,
                        "h": height,
                        "tracks": [
                            {
                                "id": track.id,
                                "conf": round(track.confidence, 2),
                                "box": [round(value, 3) for value in track.box],
                            }
                            for track in tracks
                        ],
                    }
                )
        finally:
            self.source.stop()

