"""ByteTrack wrapper with a compatibility implementation for newer supervision."""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np
import numpy.typing as npt
import supervision as sv

try:  # supervision <= 0.25 exposed the tracker from this module.
    from supervision.tracker.byte_tracker.core import ByteTrack as _SupervisionByteTrack
except ImportError:  # supervision 0.31 moved trackers to a separate project.
    _SupervisionByteTrack = getattr(sv, "ByteTrack", None)


@dataclass(frozen=True)
class Track:
    id: int
    confidence: float
    box: tuple[float, float, float, float]


@dataclass
class _TrackState:
    id: int
    box: npt.NDArray[np.float32]
    confidence: float
    hits: int = 1
    missed: int = 0


def _iou(a: npt.NDArray[np.float32], b: npt.NDArray[np.float32]) -> float:
    x1, y1 = max(a[0], b[0]), max(a[1], b[1])
    x2, y2 = min(a[2], b[2]), min(a[3], b[3])
    intersection = max(0.0, x2 - x1) * max(0.0, y2 - y1)
    area_a = max(0.0, a[2] - a[0]) * max(0.0, a[3] - a[1])
    area_b = max(0.0, b[2] - b[0]) * max(0.0, b[3] - b[1])
    return float(intersection / max(area_a + area_b - intersection, 1e-9))


class _CompatByteTrack:
    """Small IoU association fallback matching ByteTrack's confirmed-track contract."""

    def __init__(self, minimum_consecutive_frames: int = 3, lost_track_buffer: int = 30):
        self.minimum_consecutive_frames = minimum_consecutive_frames
        self.lost_track_buffer = lost_track_buffer
        self._next_id = 1
        self._tracks: list[_TrackState] = []

    def update(self, boxes: npt.NDArray[np.float32], confidences: npt.NDArray[np.float32]) -> list[Track]:
        used_tracks: set[int] = set()
        used_detections: set[int] = set()
        # ByteTrack associates high-score detections first. This also prevents a
        # low-score duplicate box from stealing an established track.
        for detection_index in np.argsort(-confidences):
            candidates = [
                (_iou(track.box, boxes[detection_index]), track_index)
                for track_index, track in enumerate(self._tracks)
                if track_index not in used_tracks
            ]
            if not candidates:
                continue
            overlap, track_index = max(candidates)
            if overlap < 0.20:
                continue
            track = self._tracks[track_index]
            track.box = boxes[detection_index].copy()
            track.confidence = float(confidences[detection_index])
            track.hits += 1
            track.missed = 0
            used_tracks.add(track_index)
            used_detections.add(detection_index)
        for index, track in enumerate(self._tracks):
            if index not in used_tracks:
                track.missed += 1
        for index, box in enumerate(boxes):
            if index not in used_detections:
                if any(_iou(track.box, box) >= 0.80 for track in self._tracks):
                    continue
                self._tracks.append(
                    _TrackState(self._next_id, box.copy(), float(confidences[index]))
                )
                self._next_id += 1
        self._tracks = [
            track for track in self._tracks if track.missed <= self.lost_track_buffer
        ]
        return [
            Track(track.id, track.confidence, tuple(map(float, track.box)))
            for track in self._tracks
            if track.missed == 0 and track.hits >= self.minimum_consecutive_frames
        ]


class PersonTracker:
    """Convert normalized person detections to confirmed, stable tracks at 10 fps."""

    def __init__(self, frame_rate: int = 10):
        self._native = (
            _SupervisionByteTrack(frame_rate=frame_rate)
            if _SupervisionByteTrack
            else None
        )
        self._fallback = _CompatByteTrack(lost_track_buffer=frame_rate * 3)

    def update(
        self,
        boxes: npt.ArrayLike,
        confidences: npt.ArrayLike,
    ) -> list[Track]:
        box_array = np.asarray(boxes, dtype=np.float32).reshape(-1, 4)
        confidence_array = np.asarray(confidences, dtype=np.float32).reshape(-1)
        if self._native is None:
            return self._fallback.update(box_array, confidence_array)
        detections = sv.Detections(
            xyxy=box_array,
            confidence=confidence_array,
            class_id=np.zeros(len(box_array), dtype=np.int32),
        )
        tracked = self._native.update_with_detections(detections)
        if tracked.tracker_id is None:
            return []
        return [
            Track(int(track_id), float(conf), tuple(map(float, box)))
            for box, conf, track_id in zip(
                tracked.xyxy, tracked.confidence, tracked.tracker_id, strict=True
            )
        ]

