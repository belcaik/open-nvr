from collections import defaultdict
from pathlib import Path

import cv2
import pytest

from opennvr_detect.model import YoloDetector
from opennvr_detect.tracker import PersonTracker


DATA = Path(__file__).parents[1] / "testdata"


@pytest.mark.skipif(
    not (DATA / "people-day.mp4").exists() or not (DATA / "yolo26n.onnx").exists(),
    reason="real test assets aren't versioned",
)
def test_real_clip_has_tracks_and_one_id_survives_twenty_consecutive_frames():
    capture = cv2.VideoCapture(str(DATA / "people-day.mp4"))
    capture.set(cv2.CAP_PROP_POS_FRAMES, 360)
    detector = YoloDetector(DATA / "yolo26n.onnx", confidence=0.25, threads=2)
    tracker = PersonTracker(frame_rate=10)
    current = defaultdict(int)
    longest = defaultdict(int)
    saw_track = False
    for _ in range(80):
        ok, frame = capture.read()
        assert ok
        boxes, confidences = detector.detect(frame)
        tracks = tracker.update(boxes, confidences)
        active = {track.id for track in tracks}
        saw_track |= bool(tracks)
        for track_id in list(current):
            if track_id not in active:
                current[track_id] = 0
        for track in tracks:
            current[track.id] += 1
            longest[track.id] = max(longest[track.id], current[track.id])
    capture.release()
    assert saw_track
    assert max(longest.values(), default=0) >= 20

