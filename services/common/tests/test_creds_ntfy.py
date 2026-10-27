import base64
import io
import os
import tempfile
import unittest
from contextlib import redirect_stdout

from opennvr_common import creds
from opennvr_common.log import log
from opennvr_common.ntfy import Notifier, build_payload
from opennvr_common.throttle import Throttle

HERE = os.path.dirname(os.path.abspath(__file__))


class CredsTest(unittest.TestCase):
    def setUp(self):
        creds.clear()

    def tearDown(self):
        creds.clear()

    def test_secret_from_file_preferred_and_redacted(self):
        with tempfile.TemporaryDirectory(dir=HERE) as d:
            path = os.path.join(d, "nvr_pass")
            with open(path, "w") as fh:
                fh.write("s3cr3t-file\n")
            env = {"NVR_PASS_FILE": path, "NVR_PASS": "from-env"}
            self.assertEqual(creds.read_secret("NVR_PASS", environ=env), "s3cr3t-file")
        self.assertEqual(creds.redact("x s3cr3t-file y"), "x *** y")

    def test_secret_from_env_when_no_file(self):
        env = {"NVR_PASS": "envpass"}
        self.assertEqual(creds.read_secret("NVR_PASS", environ=env), "envpass")
        self.assertNotIn("envpass", creds.redact("url envpass"))

    def test_missing_secret_is_empty(self):
        self.assertEqual(creds.read_secret("NTFY_TOKEN", environ={}), "")

    def test_basic_header_redacted(self):
        creds.register_basic("admin", "hunter22")
        token = base64.b64encode(b"admin:hunter22").decode()
        self.assertEqual(creds.redact(f"Authorization: Basic {token}"),
                         "Authorization: Basic ***")

    def test_log_never_prints_secret(self):
        creds.register("p4ssw0rd")
        buf = io.StringIO()
        log("fallo con p4ssw0rd en el mensaje", stream=buf)
        self.assertNotIn("p4ssw0rd", buf.getvalue())


class NtfyTest(unittest.TestCase):
    def setUp(self):
        creds.clear()

    def test_payload_is_redacted(self):
        creds.register("p4ssw0rd")
        p = build_payload("t", "título p4ssw0rd", "cuerpo p4ssw0rd", 4, ["x"])
        self.assertEqual(p["topic"], "t")
        self.assertNotIn("p4ssw0rd", p["title"] + p["message"])
        self.assertEqual(p["priority"], 4)

    def test_send_uses_sender_and_reports_suppressed(self):
        sent = []
        n = Notifier("https://ntfy.example", "topic", "tok",
                     Throttle({}, min_interval=100, max_per_hour=10),
                     sender=lambda url, payload, token: sent.append((url, payload, token)))
        with redirect_stdout(io.StringIO()):
            self.assertTrue(n.send("k", "T", "m", now=0))
            self.assertFalse(n.send("k", "T", "m", now=10))
            self.assertTrue(n.send("k", "T", "m2", now=200))
        self.assertEqual(len(sent), 2)
        self.assertEqual(sent[0][2], "tok")
        self.assertIn("1 avisos anteriores suprimidos", sent[1][1]["message"])

    def test_failed_send_can_be_retried(self):
        calls = []

        def boom(url, payload, token):
            calls.append(1)
            if len(calls) == 1:
                raise OSError("red caída")

        n = Notifier("https://ntfy.example", "t", "", Throttle({}, 100, 10), sender=boom)
        with redirect_stdout(io.StringIO()):
            self.assertFalse(n.send("k", "T", "m", now=0))
            self.assertTrue(n.send("k", "T", "m", now=1))

    def test_dry_run_without_url(self):
        n = Notifier("", "t", "", Throttle({}, 100, 10),
                     sender=lambda *a: self.fail("no debe enviar"))
        buf = io.StringIO()
        with redirect_stdout(buf):
            self.assertTrue(n.send("k", "T", "m", now=0))
        self.assertIn("ntfy desactivado", buf.getvalue())


if __name__ == "__main__":
    unittest.main()
