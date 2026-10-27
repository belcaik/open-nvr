import unittest

from nvrwatch.availability import DOWN, UP, Availability, format_duration
from nvrwatch.checks import behaviour_checks, camera_checks, gateway_check, p2p_check
from nvrwatch.config import Config
from nvrwatch.probes import http_date_drift


class AvailabilityTest(unittest.TestCase):
    def test_down_after_threshold_then_single_up(self):
        a = Availability({}, threshold=3)
        self.assertIsNone(a.update("tcp:80", False, 0, "timeout"))
        self.assertIsNone(a.update("tcp:80", False, 60, "timeout"))
        ev = a.update("tcp:80", False, 120, "timeout")
        self.assertEqual((ev.kind, ev.name), (DOWN, "tcp:80"))
        self.assertIsNone(a.update("tcp:80", False, 180), "sin avisos repetidos")
        up = a.update("tcp:80", True, 300, "abierto")
        self.assertEqual(up.kind, UP)
        self.assertEqual(up.duration, 300)
        self.assertIsNone(a.update("tcp:80", True, 360))

    def test_flap_below_threshold_is_silent(self):
        a = Availability({}, threshold=2)
        self.assertIsNone(a.update("rtsp", False, 0))
        self.assertIsNone(a.update("rtsp", True, 60))
        self.assertIsNone(a.update("rtsp", False, 120))

    def test_state_survives_restart(self):
        state = {}
        Availability(state, 1).update("http:api", False, 0)
        self.assertEqual(Availability(state, 1).down_names(), ["http:api"])
        self.assertEqual(Availability(state, 1).update("http:api", True, 90).kind, UP)

    def test_format_duration(self):
        self.assertEqual(format_duration(3725), "1 h 2 min")
        self.assertEqual(format_duration(None), "?")


class ChecksTest(unittest.TestCase):
    VALUES = {
        "net.1.Gateway.IPAddress": "192.168.0.1",
        "p2p.1.Enable": "true",
        "p2p.1.State": "internet-ready",
        "camera.1.IP": "192.168.0.14",
        "camstatus.1.LoginStatus": "connect success",
        "camstatus.2.LoginStatus": "disconnected",
    }

    def test_gateway(self):
        self.assertEqual(gateway_check(self.VALUES, "192.168.0.8")[1], False)
        self.assertEqual(gateway_check(self.VALUES, "192.168.0.1")[1], True)
        self.assertIsNone(gateway_check(self.VALUES, ""))

    def test_p2p_online_detected(self):
        name, ok, detail = p2p_check(self.VALUES, True, ["internet-ready"])
        self.assertFalse(ok)
        self.assertIn("p2p.1.State", detail)
        self.assertIsNone(p2p_check(self.VALUES, False, ["internet-ready"]))
        offline = dict(self.VALUES, **{"p2p.1.State": "offline"})
        self.assertTrue(p2p_check(offline, True, ["internet-ready"])[1])

    def test_cameras_only_registered_channels(self):
        self.assertEqual(camera_checks(self.VALUES),
                         [("camera:1", True, "LoginStatus='connect success'")])

    def test_behaviour_checks_with_drift(self):
        cfg = Config(time_drift_max=60, expected_gateway="192.168.0.8")
        checks = {name: ok for name, ok, _ in behaviour_checks(self.VALUES, 300.0, cfg)}
        self.assertEqual(checks, {"time": False, "gateway": False, "camera:1": True})

    def test_http_date_drift(self):
        headers = {"date": "Mon, 26 Oct 2026 10:00:30 GMT"}
        now = 1793008800.0  # 2026-10-26T10:00:00Z
        self.assertAlmostEqual(http_date_drift(headers, now), 30.0)
        self.assertIsNone(http_date_drift({}, now))
        self.assertIsNone(http_date_drift({"date": "basura"}, now))


if __name__ == "__main__":
    unittest.main()
