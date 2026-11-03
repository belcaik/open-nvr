import asyncio
import json
import pytest
from websockets.datastructures import Headers
from websockets.http11 import Request

from opennvr_detect.server import CameraWorker, DetectionServer


def detection_message(sequence=1):
    return {
        "type": "detections",
        "cam": 1,
        "seq": sequence,
        "ts": "2026-11-03T15:30:01.123456Z",
        "w": 800,
        "h": 448,
        "tracks": [{"id": 7, "conf": 0.83, "box": [0.412, 0.130, 0.501, 0.774]}],
    }


def assert_detection_contract(message):
    assert set(message) == {"type", "cam", "seq", "ts", "w", "h", "tracks"}
    assert message["type"] == "detections"
    assert type(message["cam"]) is int
    assert type(message["seq"]) is int
    assert type(message["ts"]) is str and message["ts"].endswith("Z")
    assert type(message["w"]) is int and type(message["h"]) is int
    assert type(message["tracks"]) is list
    for track in message["tracks"]:
        assert set(track) == {"id", "conf", "box"}
        assert type(track["id"]) is int
        assert type(track["conf"]) is float
        assert type(track["box"]) is list and len(track["box"]) == 4
        assert all(type(value) is float and 0 <= value <= 1 for value in track["box"])


class MemoryWebSocket:
    def __init__(self, blocked=False):
        self.request = Request("/detections", Headers())
        self.sent = []
        self.incoming = asyncio.Queue()
        self.blocked = blocked
        self.release = asyncio.Event()
        self.closed = False

    async def send(self, message):
        if self.blocked:
            await self.release.wait()
        self.sent.append(message)

    async def recv(self):
        return await self.incoming.get()

    async def close(self, **_kwargs):
        self.closed = True


async def attach(server, connection):
    task = asyncio.create_task(server._websocket(connection))
    while not server._clients:
        await asyncio.sleep(0)
    return task


@pytest.mark.asyncio
async def test_websocket_client_receives_message_matching_contract():
    server = DetectionServer("127.0.0.1", 0)
    client = MemoryWebSocket()
    task = await attach(server, client)
    server.publish(detection_message())
    while not client.sent:
        await asyncio.sleep(0)
    message = json.loads(client.sent[0])
    task.cancel()
    await asyncio.gather(task, return_exceptions=True)
    assert_detection_contract(message)


@pytest.mark.asyncio
async def test_non_reading_websocket_client_does_not_block_another_client():
    server = DetectionServer("127.0.0.1", 0, queue_size=2)
    slow, fast = MemoryWebSocket(blocked=True), MemoryWebSocket()
    slow_task = await attach(server, slow)
    fast_task = asyncio.create_task(server._websocket(fast))
    while len(server._clients) < 2:
        await asyncio.sleep(0)
    for sequence in range(100):
        server.publish(detection_message(sequence))
    while not fast.sent:
        await asyncio.sleep(0)
    received = json.loads(fast.sent[0])
    assert received["type"] == "detections"
    slow_task.cancel()
    fast_task.cancel()
    await asyncio.gather(slow_task, fast_task, return_exceptions=True)


@pytest.mark.asyncio
async def test_healthz_returns_200_with_latest_camera_status():
    server = DetectionServer("127.0.0.1", 0)
    status = {
        "type": "status",
        "cam": 1,
        "state": "ok",
        "fps": 9.7,
        "infer_ms_p50": 18.2,
        "dropped": 12,
    }
    server.publish_status(status)

    response = server._http_request(None, Request("/healthz", Headers()))
    assert response is not None
    code = response.status_code
    payload = json.loads(response.body)
    assert code == 200
    assert payload == [status]


class SourceDouble:
    def __init__(self):
        self.state = "starting"
        self.dropped = 0
        self.starts = 0
        self.stops = 0

    def start(self):
        self.starts += 1
        self.state = "ok"

    def stop(self):
        self.stops += 1
        self.state = "stopped"

    def get_latest(self, _timeout):
        return None


async def wait_until(predicate):
    deadline = asyncio.get_running_loop().time() + 1
    while not predicate() and asyncio.get_running_loop().time() < deadline:
        await asyncio.sleep(0.01)
    assert predicate()


@pytest.mark.asyncio
async def test_cameras_message_disables_and_reenables_worker():
    server = DetectionServer("127.0.0.1", 0)
    client = MemoryWebSocket()
    connection_task = await attach(server, client)
    source = SourceDouble()
    worker = CameraWorker(1, source, None, None, server)
    stop = asyncio.Event()
    worker_task = asyncio.create_task(worker.run(stop))
    try:
        await wait_until(lambda: source.starts == 1)
        await client.incoming.put(json.dumps({"type": "cameras", "enabled": []}))
        await wait_until(lambda: source.stops == 1)
        assert server.statuses[1]["state"] == "disabled"
        assert server.statuses[1]["fps"] == 0
        response = server._http_request(None, Request("/healthz", Headers()))
        assert response.status_code == 200
        assert json.loads(response.body)[0]["state"] == "disabled"

        await client.incoming.put(json.dumps({"type": "cameras", "enabled": [1]}))
        await wait_until(lambda: source.starts == 2)
        assert server.is_enabled(1)
    finally:
        stop.set()
        await asyncio.gather(worker_task, return_exceptions=True)
        connection_task.cancel()
        await asyncio.gather(connection_task, return_exceptions=True)


@pytest.mark.asyncio
async def test_invalid_cameras_message_is_logged_without_closing_connection(caplog):
    server = DetectionServer("127.0.0.1", 0)
    client = MemoryWebSocket()
    task = await attach(server, client)
    try:
        await client.incoming.put(json.dumps({"type": "cameras", "enabled": "1"}))
        await client.incoming.put(json.dumps({"type": "cameras", "enabled": []}))
        await wait_until(lambda: not server.is_enabled(1))
        assert not client.closed
        assert not task.done()
        assert "invalid cameras control message" in caplog.text
    finally:
        task.cancel()
        await asyncio.gather(task, return_exceptions=True)


@pytest.mark.asyncio
async def test_all_configured_cameras_run_without_control_message():
    server = DetectionServer("127.0.0.1", 0)
    sources = [SourceDouble(), SourceDouble()]
    stop = asyncio.Event()
    tasks = [
        asyncio.create_task(CameraWorker(camera, source, None, None, server).run(stop))
        for camera, source in enumerate(sources, start=1)
    ]
    try:
        await wait_until(lambda: all(source.starts == 1 for source in sources))
        assert set(server.statuses) == {1, 2}
        assert all(status["state"] == "ok" for status in server.statuses.values())
    finally:
        stop.set()
        await asyncio.gather(*tasks, return_exceptions=True)

