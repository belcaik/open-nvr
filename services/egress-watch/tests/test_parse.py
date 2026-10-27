import json
import unittest

from egresswatch.parse import parse_dnsmasq_line, parse_prefix, parse_ulog_line


class UlogParseTest(unittest.TestCase):
    def test_json_short_keys(self):
        line = json.dumps({"timestamp": "2026-10-26T10:00:00", "oob.prefix": "opennvr-drop-out ",
                           "src_ip": "192.168.0.227", "dest_ip": "203.0.113.5",
                           "src_port": 40000, "dest_port": 8090, "ip.protocol": 6,
                           "ip.totlen": 60})
        f = parse_ulog_line(line)
        self.assertEqual((f.action, f.direction, f.dst, f.dport, f.proto, f.length),
                         ("drop", "out", "203.0.113.5", 8090, "tcp", 60))

    def test_json_long_keys(self):
        line = json.dumps({"oob.prefix": "opennvr-fwd-in", "ip.saddr.str": "203.0.113.5",
                           "ip.daddr.str": "192.168.0.227", "udp.sport": 123,
                           "udp.dport": 40001, "ip.protocol": 17, "ip.totlen": "76"})
        f = parse_ulog_line(line)
        self.assertEqual((f.action, f.direction, f.src, f.sport, f.proto, f.length),
                         ("fwd", "in", "203.0.113.5", 123, "udp", 76))

    def test_icmp_without_ports(self):
        f = parse_ulog_line(json.dumps({"oob.prefix": "opennvr-drop-out", "src_ip": "192.168.0.227",
                                        "dest_ip": "1.1.1.1", "ip.protocol": 1, "ip.totlen": 84}))
        self.assertEqual((f.proto, f.dport), ("icmp", 0))

    def test_foreign_or_broken_lines_ignored(self):
        self.assertIsNone(parse_ulog_line(json.dumps({"oob.prefix": "other-thing"})))
        self.assertIsNone(parse_ulog_line("{not json"))
        self.assertIsNone(parse_ulog_line(""))
        self.assertIsNone(parse_ulog_line("[1,2]"))

    def test_prefix(self):
        self.assertEqual(parse_prefix("opennvr-drop-v6 "), ("drop", "v6"))
        self.assertIsNone(parse_prefix("opennvr-x"))


class DnsmasqParseTest(unittest.TestCase):
    def test_query(self):
        ev = parse_dnsmasq_line("Oct 26 10:00:00 dnsmasq[1]: 12 192.168.0.227/41234 "
                                "query[A] erp.zwcloud.wang from 192.168.0.227")
        self.assertEqual((ev.kind, ev.name, ev.client, ev.qtype),
                         ("query", "erp.zwcloud.wang", "192.168.0.227", "A"))

    def test_reply_and_nxdomain(self):
        ev = parse_dnsmasq_line("Oct 26 10:00:00 dnsmasq[1]: 12 192.168.0.227/41234 "
                                "reply pool.ntp.org is 203.0.113.7")
        self.assertEqual((ev.kind, ev.name, ev.value), ("reply", "pool.ntp.org", "203.0.113.7"))
        ev = parse_dnsmasq_line("Oct 26 10:00:00 dnsmasq[1]: 13 192.168.0.227/41235 "
                                "config utilerp.p6sai.com is NXDOMAIN")
        self.assertEqual((ev.kind, ev.value), ("config", "NXDOMAIN"))

    def test_other_lines(self):
        self.assertIsNone(parse_dnsmasq_line("dnsmasq[1]: started, version 2.90 cachesize 0"))


if __name__ == "__main__":
    unittest.main()
