import datetime
import unittest

from egresswatch.aggregate import AggConfig, Aggregator, flow_key
from egresswatch.parse import Dns, Flow

NVR = "192.168.0.227"


def out(dst, dport, length=100, action="drop", proto="tcp"):
    return Flow(action, "out", NVR, dst, proto, 40000, dport, length)


def reply(src, sport, length=100):
    return Flow("fwd", "in", src, NVR, "tcp", sport, 40000, length)


MORNING = datetime.datetime(2026, 10, 26, 7, 0)
TODAY = MORNING.date().isoformat()


class AggregatorTest(unittest.TestCase):
    def make(self, **kw):
        cfg = AggConfig(window_seconds=300, spike_factor=5, spike_min_bytes=1000,
                        summary_hour=8, silence_hours=1, **kw)
        return Aggregator({}, cfg, now=0, today=TODAY)

    def test_new_destinations_batched_once(self):
        agg = self.make()
        agg.flow(out("203.0.113.5", 8090), 1)
        agg.flow(out("203.0.113.5", 8090), 2)
        agg.flow(out("203.0.113.6", 32100, proto="udp"), 3)
        alerts = agg.tick(4, MORNING)
        new = [a for a in alerts if a.key.startswith("new:")]
        self.assertEqual(len(new), 1)
        self.assertIn("2 destinos nuevos", new[0].title)
        self.assertIn("bloqueado hacia 203.0.113.5:8090/tcp", new[0].message)
        agg.flow(out("203.0.113.5", 8090), 5)
        self.assertEqual([a for a in agg.tick(6, MORNING) if a.key.startswith("new:")], [])

    def test_reply_counts_as_same_flow(self):
        self.assertEqual(flow_key(out("203.0.113.5", 443, action="fwd")),
                         flow_key(reply("203.0.113.5", 443)))

    def test_domain_from_dns_and_new_domain(self):
        agg = self.make()
        agg.dns(Dns("query", "erp.zwcloud.wang", None, NVR, "A"), 1)
        agg.dns(Dns("reply", "erp.zwcloud.wang", "203.0.113.9", None, None), 1)
        agg.dns(Dns("query", "other.example", None, "192.168.0.50", "A"), 1)  # otro cliente
        agg.flow(out("203.0.113.9", 8090), 2)
        msg = agg.tick(3, MORNING)[0].message
        self.assertIn("DNS consulta erp.zwcloud.wang", msg)
        self.assertIn("203.0.113.9:8090/tcp (erp.zwcloud.wang)", msg)
        self.assertNotIn("other.example", msg)

    def test_spike_detection(self):
        agg = self.make()
        # Ventanas tranquilas de 200 B establecen la media móvil.
        for w in range(5):
            agg.flow(out("203.0.113.5", 443, 200, action="fwd"), w * 300 + 10)
            agg.tick(w * 300 + 20, MORNING)
        agg.tick(5 * 300 + 1, MORNING)
        agg.flow(out("203.0.113.5", 443, 50_000, action="fwd"), 5 * 300 + 10)
        alerts = agg.tick(6 * 300 + 1, MORNING)
        spikes = [a for a in alerts if a.key == "spike"]
        self.assertEqual(len(spikes), 1)
        self.assertIn("203.0.113.5:443/tcp", spikes[0].message)

    def test_dropped_traffic_never_spikes(self):
        agg = self.make()
        agg.flow(out("203.0.113.5", 443, 10_000_000), 10)
        self.assertEqual([a for a in agg.tick(301, MORNING) if a.key == "spike"], [])

    def test_long_gap_does_not_loop_forever(self):
        agg = self.make()
        agg.tick(10 * 86400, MORNING)
        self.assertEqual(agg.state["window"]["start"], 10 * 86400)

    def test_silence_alert_and_resume(self):
        agg = self.make()
        alerts = agg.tick(3601, MORNING)
        self.assertEqual([a.key for a in alerts], ["silence"])
        self.assertEqual([a.key for a in agg.tick(7200, MORNING) if a.key == "silence"], [])
        self.assertEqual([a.key for a in agg.flow(out("203.0.113.5", 1), 7300)], ["silence:end"])

    def test_daily_summary_once_after_hour(self):
        agg = self.make(nvr_ip=NVR)
        agg.flow(out("203.0.113.5", 8090), 1)
        agg.dns(Dns("query", "utilerp.p6sai.com", None, NVR, "A"), 1)
        agg.dns(Dns("config", "utilerp.p6sai.com", "NXDOMAIN", None, None), 1)
        self.assertFalse([a for a in agg.tick(2, MORNING) if a.key.startswith("summary:")])
        nine = MORNING.replace(hour=9)
        tomorrow = (nine + datetime.timedelta(days=1))
        # Mismo día que el inicio: ya se considera hecho. Al día siguiente, una vez.
        self.assertFalse([a for a in agg.tick(3, nine) if a.key.startswith("summary:")])
        summaries = [a for a in agg.tick(4, tomorrow) if a.key.startswith("summary:")]
        self.assertEqual(len(summaries), 1)
        self.assertIn("Bloqueado: 1 paquetes", summaries[0].message)
        self.assertIn("utilerp.p6sai.com  (NXDOMAIN)", summaries[0].message)
        self.assertFalse([a for a in agg.tick(5, tomorrow) if a.key.startswith("summary:")])
        self.assertEqual(agg.state["daily"]["drop_packets"], 0)


if __name__ == "__main__":
    unittest.main()
