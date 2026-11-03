"""Entrypoint for ``python -m opennvr_detect``."""

from __future__ import annotations

import asyncio
import logging
import os
import signal

from .model import YoloDetector
from .server import CameraWorker, DetectionServer
from .source import FrameSource
from .tracker import PersonTracker


def parse_cameras(value: str) -> list[tuple[int, str]]:
    cameras: list[tuple[int, str]] = []
    for item in value.split(","):
        if not item.strip():
            continue
        camera, separator, url = item.partition("=")
        if not separator or not camera.strip().isdigit() or not url:
            raise ValueError("DETECT_CAMERAS must contain comma-separated <number>=<source>")
        cameras.append((int(camera), url))
    if not cameras:
        raise ValueError("DETECT_CAMERAS must configure at least one camera")
    return cameras


def parse_listen(value: str) -> tuple[str, int]:
    host, separator, port = value.rpartition(":")
    if not separator or not host:
        raise ValueError("DETECT_LISTEN must be <host>:<port>")
    return host, int(port)


async def main() -> None:
    logging.basicConfig(
        level=os.getenv("LOG_LEVEL", "INFO"),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    cameras = parse_cameras(os.environ["DETECT_CAMERAS"])
    model_path = os.environ["DETECT_MODEL"]
    confidence = float(os.getenv("DETECT_CONF", "0.25"))
    threads = int(os.getenv("DETECT_THREADS", "2"))
    host, port = parse_listen(os.getenv("DETECT_LISTEN", "127.0.0.1:8556"))
    server = DetectionServer(host, port)
    await server.start()
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for signum in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(signum, stop.set)
    workers = [
        CameraWorker(
            camera,
            FrameSource(camera, source),
            YoloDetector(model_path, confidence, threads),
            PersonTracker(frame_rate=10),
            server,
        )
        for camera, source in cameras
    ]
    tasks = [asyncio.create_task(worker.run(stop)) for worker in workers]
    try:
        await stop.wait()
    finally:
        stop.set()
        await asyncio.gather(*tasks, return_exceptions=True)
        await server.close()


if __name__ == "__main__":
    asyncio.run(main())

