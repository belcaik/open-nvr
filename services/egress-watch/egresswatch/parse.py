"""Parseo de las líneas de ulogd2 (JSON) y del registro de consultas de dnsmasq.

ulogd2 JSON: los nombres de campo dependen de la versión del plugin. Se
aceptan las dos variantes conocidas (`src_ip`/`dest_ip`/`src_port`/
`dest_port` y `ip.saddr.str`/`ip.daddr.str`/`tcp.sport`...). Pendiente de
confirmar con una línea real de Baphomet (ver docs/monitoring.md).
"""
import json
import re
from collections import namedtuple

Flow = namedtuple("Flow", "action direction src dst proto sport dport length")
Dns = namedtuple("Dns", "kind name value client qtype")

PROTOS = {1: "icmp", 6: "tcp", 17: "udp", 58: "icmpv6", 132: "sctp"}

_KEYS = {
    "src": ("src_ip", "ip.saddr.str", "ip6.saddr.str", "ip.saddr"),
    "dst": ("dest_ip", "ip.daddr.str", "ip6.daddr.str", "ip.daddr"),
    "sport": ("src_port", "tcp.sport", "udp.sport", "sctp.sport"),
    "dport": ("dest_port", "tcp.dport", "udp.dport", "sctp.dport"),
    "proto": ("ip.protocol", "ip6.nexthdr", "protocol"),
    "length": ("ip.totlen", "ip6.payloadlen", "raw.pktlen", "oob.len"),
    "prefix": ("oob.prefix", "prefix"),
}


def _get(obj, field):
    for key in _KEYS[field]:
        if key in obj and obj[key] not in (None, ""):
            return obj[key]
    return None


def _int(value, default=0):
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def parse_prefix(prefix):
    """'opennvr-fwd-out ' -> ('fwd', 'out'). None si no es de open-nvr."""
    parts = str(prefix).strip().split("-")
    if len(parts) < 3 or parts[0] != "opennvr":
        return None
    return parts[1], parts[2]


def parse_ulog_line(line):
    line = line.strip()
    if not line:
        return None
    try:
        obj = json.loads(line)
    except json.JSONDecodeError:
        return None
    if not isinstance(obj, dict):
        return None
    tag = parse_prefix(_get(obj, "prefix") or "")
    if tag is None:
        return None
    proto = _get(obj, "proto")
    proto = PROTOS.get(_int(proto, -1), str(proto)) if proto is not None else "?"
    length = _int(_get(obj, "length"))
    return Flow(action=tag[0], direction=tag[1],
                src=str(_get(obj, "src") or "?"), dst=str(_get(obj, "dst") or "?"),
                proto=proto, sport=_int(_get(obj, "sport")), dport=_int(_get(obj, "dport")),
                length=length)


_QUERY = re.compile(r"query\[(?P<qtype>[A-Za-z0-9]+)\] (?P<name>\S+) from (?P<client>\S+)")
_ANSWER = re.compile(r"\b(?P<kind>reply|cached|config|forwarded)\s+(?P<name>\S+)\s+(?:is|to)\s+(?P<value>\S+)")


def parse_dnsmasq_line(line):
    """Interpreta una línea de `log-queries=extra` de dnsmasq."""
    m = _QUERY.search(line)
    if m:
        return Dns("query", m["name"].lower().rstrip("."), None, m["client"], m["qtype"])
    m = _ANSWER.search(line)
    if m:
        return Dns(m["kind"], m["name"].lower().rstrip("."), m["value"], None, None)
    return None
