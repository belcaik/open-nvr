import io
import os
import tempfile
import unittest
from contextlib import redirect_stdout

from opennvr_common import statefile
from opennvr_common.ntfy import Notifier
from opennvr_common.throttle import Throttle

from nvrwatch import app
from nvrwatch.config import Config

HERE = os.path.dirname(os.path.abspath(__file__))


class AppBaselineTest(unittest.TestCase):
    """Flujo de línea base sin red: creación, sección nueva, cambio, dedup, accept."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=HERE)
        self.cfg = Config(data_dir=self.tmp.name, baseline_realert=86400)
        self.sent = []
        notifier = Notifier("https://ntfy.example", "t", "", Throttle({}, 3600, 100),
                            sender=lambda url, payload, token: self.sent.append(payload),
                            prefix="NVR: ")
        self.app = app.App(self.cfg, notifier=notifier)
        self.out = io.StringIO()

    def tearDown(self):
        self.tmp.cleanup()

    def compare(self, values, sections, now):
        with redirect_stdout(self.out):
            return self.app.compare_baseline(values, sections, now)

    def test_full_cycle(self):
        v1 = {"device.SoftWareVersion": "V1", "p2p.1.Enable": "true"}
        self.assertEqual(self.compare(v1, {"device", "p2p"}, 0), [])
        self.assertEqual(self.sent[-1]["title"], "NVR: línea base creada")

        # El escaneo de puertos llega más tarde: la sección se incorpora sin aviso.
        v2 = dict(v1, **{"port.80": "open"})
        self.assertEqual(self.compare(v2, {"device", "p2p", "port"}, 100), [])
        self.assertEqual(len(self.sent), 1)

        # Cambio real: aviso una vez; repetido dentro de BASELINE_REALERT, suprimido.
        v3 = dict(v2, **{"p2p.1.Enable": "false", "port.23": "open"})
        changes = self.compare(v3, {"device", "p2p", "port"}, 200)
        self.assertEqual(len(changes), 2)
        self.assertEqual(len(self.sent), 2)
        self.assertIn("p2p.1.Enable", self.sent[-1]["message"])
        self.compare(v3, {"device", "p2p", "port"}, 1100)
        self.assertEqual(len(self.sent), 2)

        # accept toma la última instantánea como nueva línea base.
        statefile.save(os.path.join(self.tmp.name, "last_snapshot.json"),
                       {"ts": 1100, "sections": ["device", "p2p", "port"], "values": v3})
        with redirect_stdout(self.out):
            self.assertEqual(app.accept(self.cfg), 0)
        self.assertEqual(self.compare(v3, {"device", "p2p", "port"}, 1200), [])


if __name__ == "__main__":
    unittest.main()
