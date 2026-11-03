import logging
import threading
import time

from opennvr_detect.source import FrameSource


class CaptureDouble:
    instances = []
    block_opening = False

    def __init__(self):
        self.opened = threading.Event()
        self.released = threading.Event()
        self.release_calls = 0
        self.__class__.instances.append(self)

    def open(self, *_args):
        self.opened.set()
        if self.block_opening:
            self.released.wait()

    def isOpened(self):
        return False

    def release(self):
        self.release_calls += 1
        self.released.set()


def install_capture_double(monkeypatch, blocked=False):
    CaptureDouble.instances = []
    CaptureDouble.block_opening = blocked
    monkeypatch.setattr("opennvr_detect.source.cv2.VideoCapture", CaptureDouble)


def wait_for_capture(capture):
    assert capture.opened.wait(1)


def test_missing_source_reconnects_without_raising_or_logging_url(caplog):
    secret_url = "rtsp://user:very-secret@127.0.0.1:1/does-not-exist"
    source = FrameSource(7, secret_url)
    with caplog.at_level(logging.INFO):
        source.start()
        deadline = time.monotonic() + 3
        while source.state != "reconnecting" and time.monotonic() < deadline:
            time.sleep(0.02)
        source.stop()
    assert source.state == "reconnecting"
    assert secret_url not in caplog.text
    assert "very-secret" not in caplog.text


def test_source_reopens_capture_when_started_after_stop(monkeypatch):
    install_capture_double(monkeypatch)
    source = FrameSource(1, "file:unused.mp4")
    source.start()
    wait_for_capture(CaptureDouble.instances[0])
    source.stop()
    source.start()
    deadline = time.monotonic() + 1
    while len(CaptureDouble.instances) < 2 and time.monotonic() < deadline:
        time.sleep(0.01)
    assert len(CaptureDouble.instances) == 2
    wait_for_capture(CaptureDouble.instances[1])
    source.stop()


def test_stop_releases_capture_blocked_opening_in_less_than_five_seconds(monkeypatch):
    install_capture_double(monkeypatch, blocked=True)
    source = FrameSource(1, "rtsp://127.0.0.1/unused")
    source.start()
    wait_for_capture(CaptureDouble.instances[0])
    started = time.monotonic()
    source.stop()
    elapsed = time.monotonic() - started
    capture = CaptureDouble.instances[0]
    assert capture.release_calls > 0
    assert elapsed < 5

