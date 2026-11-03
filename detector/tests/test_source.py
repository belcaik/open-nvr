import logging
import time

from opennvr_detect.source import FrameSource


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

