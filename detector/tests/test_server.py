import asyncio
import json
import pytest
from websockets.datastructures import Headers
from websockets.http11 import Request

from opennvr_detect.server import DetectionServer


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
        self.blocked = blocked
        self.release = asyncio.Event()

    async def send(self, message):
        if self.blocked:
            await self.release.wait()
        self.sent.append(message)

    async def close(self, **_kwargs):
        return None


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

