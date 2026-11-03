"""YOLO26 end-to-end ONNX inference and coordinate conversion."""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any

import cv2
import numpy as np
import numpy.typing as npt
import onnxruntime as ort

PERSON_CLASS = 0


@dataclass(frozen=True)
class LetterboxTransform:
    scale: float
    pad_x: float
    pad_y: float
    original_width: int
    original_height: int


def letterbox(
    image: npt.NDArray[np.uint8], size: int = 416
) -> tuple[npt.NDArray[np.uint8], LetterboxTransform]:
    """Resize a BGR image into a square canvas without changing its aspect ratio."""
    height, width = image.shape[:2]
    scale = min(size / width, size / height)
    resized_width = max(1, round(width * scale))
    resized_height = max(1, round(height * scale))
    resized = cv2.resize(
        image, (resized_width, resized_height), interpolation=cv2.INTER_LINEAR
    )
    left = (size - resized_width) // 2
    top = (size - resized_height) // 2
    canvas = np.full((size, size, 3), 114, dtype=np.uint8)
    canvas[top : top + resized_height, left : left + resized_width] = resized
    return canvas, LetterboxTransform(
        scale, float(left), float(top), width, height
    )


def restore_boxes(
    boxes: npt.ArrayLike, transform: LetterboxTransform
) -> npt.NDArray[np.float32]:
    """Map xyxy boxes from letterbox pixels to original-frame pixels."""
    restored = np.asarray(boxes, dtype=np.float32).copy().reshape(-1, 4)
    restored[:, [0, 2]] = (restored[:, [0, 2]] - transform.pad_x) / transform.scale
    restored[:, [1, 3]] = (restored[:, [1, 3]] - transform.pad_y) / transform.scale
    restored[:, [0, 2]] = restored[:, [0, 2]].clip(0, transform.original_width)
    restored[:, [1, 3]] = restored[:, [1, 3]].clip(0, transform.original_height)
    return restored


def normalize_boxes(
    boxes: npt.ArrayLike, width: int, height: int
) -> npt.NDArray[np.float32]:
    result = np.asarray(boxes, dtype=np.float32).copy().reshape(-1, 4)
    result[:, [0, 2]] /= width
    result[:, [1, 3]] /= height
    return result.clip(0.0, 1.0)


def decode_output(
    output: npt.ArrayLike,
    transform: LetterboxTransform,
    confidence: float,
) -> tuple[npt.NDArray[np.float32], npt.NDArray[np.float32]]:
    """Decode [1,N,6] xyxy/score/class output into original normalized boxes."""
    rows = np.asarray(output, dtype=np.float32)
    while rows.ndim > 2 and rows.shape[0] == 1:
        rows = rows[0]
    if rows.ndim != 2 or rows.shape[1] != 6:
        raise ValueError(f"expected ONNX output [1,N,6], got {np.asarray(output).shape}")
    selected = rows[(rows[:, 4] >= confidence) & (rows[:, 5].astype(np.int64) == PERSON_CLASS)]
    if not len(selected):
        return np.empty((0, 4), np.float32), np.empty((0,), np.float32)
    pixel_boxes = restore_boxes(selected[:, :4], transform)
    boxes = normalize_boxes(
        pixel_boxes, transform.original_width, transform.original_height
    )
    return boxes, selected[:, 4].copy()


class YoloDetector:
    """A CPU-only ONNX Runtime session for the supplied YOLO26n-e2e model."""

    def __init__(self, model_path: str | Path, confidence: float = 0.25, threads: int = 2):
        self.model_path = str(model_path)
        self.threads = threads
        self.confidence = confidence
        self.size = 416
        self.session: Any = None
        self.input_name = "images"
        self.output_name = "output0"

    def _initialize(self) -> None:
        if self.session is not None:
            return
        options = ort.SessionOptions()
        options.intra_op_num_threads = self.threads
        options.inter_op_num_threads = 1
        options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        self.session = ort.InferenceSession(
            self.model_path, sess_options=options, providers=["CPUExecutionProvider"]
        )
        input_meta = self.session.get_inputs()[0]
        self.input_name = input_meta.name
        self.output_name = self.session.get_outputs()[0].name
        self.size = int(input_meta.shape[-1])

    def detect(
        self, frame: npt.NDArray[np.uint8]
    ) -> tuple[npt.NDArray[np.float32], npt.NDArray[np.float32]]:
        self._initialize()
        padded, transform = letterbox(frame, self.size)
        tensor = padded[:, :, ::-1].transpose(2, 0, 1)
        tensor = np.ascontiguousarray(tensor, dtype=np.float32) / 255.0
        output = self.session.run(
            [self.output_name], {self.input_name: tensor[None]}
        )[0]
        return decode_output(output, transform, self.confidence)

