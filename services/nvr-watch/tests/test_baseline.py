import unittest

from nvrwatch import baseline
from nvrwatch.baseline import ADDED, CHANGED, REMOVED


class DiffTest(unittest.TestCase):
    BASE = {
        "device.SoftWareVersion": "V1.0",
        "camera.1.IP": "192.168.0.14",
        "camera.1.MAC": "aa:bb",
        "port.80": "open",
        "port.554": "open",
        "status.MainStreamVideoBitrate": "2048",
    }

    def test_no_changes(self):
        self.assertEqual(baseline.diff(self.BASE, dict(self.BASE),
                                       {"device", "camera", "port"}), [])

    def test_added_removed_changed(self):
        cur = dict(self.BASE)
        cur["device.SoftWareVersion"] = "V2.0"
        del cur["port.554"]
        cur["port.23"] = "open"
        changes = baseline.diff(self.BASE, cur, {"device", "camera", "port"})
        self.assertEqual(
            [(c.kind, c.key) for c in changes],
            [(CHANGED, "device.SoftWareVersion"), (ADDED, "port.23"), (REMOVED, "port.554")])
        self.assertEqual(changes[0].old, "V1.0")
        self.assertEqual(changes[0].new, "V2.0")

    def test_missing_section_does_not_report_removals(self):
        cur = {k: v for k, v in self.BASE.items() if not k.startswith("camera.")}
        changes = baseline.diff(self.BASE, cur, {"device", "port"})
        self.assertEqual(changes, [])

    def test_ignore_prefixes(self):
        cur = dict(self.BASE, **{"status.MainStreamVideoBitrate": "10"})
        self.assertEqual(baseline.diff(self.BASE, cur, {"status"}, ignore_prefixes=("status.",)),
                         [])
        self.assertEqual(len(baseline.diff(self.BASE, cur, {"status"})), 1)

    def test_fingerprint_stable_and_order_independent(self):
        cur = dict(self.BASE, **{"port.23": "open", "device.SoftWareVersion": "V2"})
        a = baseline.diff(self.BASE, cur, {"device", "port"})
        self.assertEqual(baseline.fingerprint(a), baseline.fingerprint(list(reversed(a))))
        other = baseline.diff(self.BASE, dict(self.BASE, **{"port.22": "open"}), {"port"})
        self.assertNotEqual(baseline.fingerprint(a), baseline.fingerprint(other))

    def test_merge_replaces_only_given_sections(self):
        cur = {"port.80": "open", "camera.2.IP": "192.168.0.15"}
        merged = baseline.merge(self.BASE, cur, {"port"})
        self.assertNotIn("port.554", merged)
        self.assertEqual(merged["camera.1.IP"], "192.168.0.14")
        self.assertNotIn("camera.2.IP", merged)

    def test_format_changes_limit(self):
        cur = {f"port.{p}": "open" for p in range(100)}
        text = baseline.format_changes(baseline.diff({}, cur, {"port"}), limit=5)
        self.assertIn("95 cambios más", text)


if __name__ == "__main__":
    unittest.main()
