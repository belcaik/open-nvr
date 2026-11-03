from types import SimpleNamespace

import numpy as np

from opennvr_detect.model import YoloDetector, decode_output, letterbox, restore_boxes


class SessionDouble:
    def __init__(self, shape):
        self.shape = shape

    def get_inputs(self):
        return [SimpleNamespace(name="images", shape=self.shape)]

    def get_outputs(self):
        return [SimpleNamespace(name="output0")]


def initialize_with_shape(monkeypatch, shape):
    monkeypatch.setattr(
        "opennvr_detect.model.ort.InferenceSession",
        lambda *_args, **_kwargs: SessionDouble(shape),
    )
    detector = YoloDetector("unused.onnx")
    detector._initialize()
    return detector


def test_model_reads_static_input_size_from_onnx(monkeypatch):
    detector = initialize_with_shape(monkeypatch, [1, 3, 640, 640])
    assert detector.size == 640


def test_model_uses_416_when_input_shape_is_dynamic(monkeypatch):
    detector = initialize_with_shape(monkeypatch, [1, 3, "height", "width"])
    assert detector.size == 416


def test_letterbox_round_trip_recovers_known_box_with_subpixel_error():
    image = np.zeros((448, 800, 3), dtype=np.uint8)
    _, transform = letterbox(image, 416)
    original = np.array([[123.4, 45.6, 701.2, 401.8]], dtype=np.float32)
    encoded = original.copy()
    encoded[:, [0, 2]] = encoded[:, [0, 2]] * transform.scale + transform.pad_x
    encoded[:, [1, 3]] = encoded[:, [1, 3]] * transform.scale + transform.pad_y
    recovered = restore_boxes(encoded, transform)
    assert np.max(np.abs(recovered - original)) < 1.0


def test_decode_rejects_non_person_classes_and_scores_below_threshold():
    image = np.zeros((100, 200, 3), dtype=np.uint8)
    _, transform = letterbox(image, 416)
    output = np.array(
        [[[20, 120, 100, 220, 0.90, 0], [20, 120, 100, 220, 0.99, 2], [20, 120, 100, 220, 0.24, 0]]],
        dtype=np.float32,
    )
    boxes, scores = decode_output(output, transform, 0.25)
    assert boxes.shape == (1, 4)
    assert scores.tolist() == [np.float32(0.9)]
    assert np.all((0 <= boxes) & (boxes <= 1))

