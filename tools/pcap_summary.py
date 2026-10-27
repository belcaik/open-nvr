#!/usr/bin/env python3
"""Resume un pcap Ethernet: flujos IPv4/IPv6, nombres DNS y destinos públicos.

Sin dependencias (no requiere tshark). Pensado para auditar qué contacta el
cliente EasyVideo desde la VM y qué intercambia con el NVR.

    tools/pcap_summary.py captures/vm/win10-x.pcap
    tools/pcap_summary.py captures/vm/win10-x.pcap --host 192.168.0.227
    tools/pcap_summary.py captures/vm/win10-x.pcap --dump-tcp 192.168.0.227:6060 out.bin

--dump-tcp extrae el payload TCP de los flujos con ese extremo, en orden de
captura y con una cabecera de texto por segmento, para analizar el protocolo
privado sin enviarle nada al equipo.
"""
import argparse
import collections
import ipaddress
import struct
import sys

LINKTYPE_ETHERNET = 1


def read_pcap(path):
    with open(path, "rb") as f:
        head = f.read(24)
        magic = head[:4]
        if magic in (b"\xd4\xc3\xb2\xa1", b"\x4d\x3c\xb2\xa1"):
            endian = "<"
        elif magic in (b"\xa1\xb2\xc3\xd4", b"\xa1\xb2\x3c\x4d"):
            endian = ">"
        else:
            raise SystemExit(f"{path}: no es un pcap clásico")
        nano = magic in (b"\x4d\x3c\xb2\xa1", b"\xa1\xb2\x3c\x4d")
        linktype = struct.unpack(endian + "I", head[20:24])[0]
        if linktype != LINKTYPE_ETHERNET:
            raise SystemExit(f"linktype {linktype} no soportado")
        while True:
            rec = f.read(16)
            if len(rec) < 16:
                return
            sec, frac, incl, _ = struct.unpack(endian + "IIII", rec)
            data = f.read(incl)
            yield sec + frac / (1e9 if nano else 1e6), data


def parse(frame):
    """Devuelve (proto, src, sport, dst, dport, payload) o None."""
    if len(frame) < 14:
        return None
    etype = struct.unpack("!H", frame[12:14])[0]
    off = 14
    if etype == 0x8100 and len(frame) >= 18:
        etype = struct.unpack("!H", frame[16:18])[0]
        off = 18
    if etype == 0x0800:
        ihl = (frame[off] & 0x0F) * 4
        proto = frame[off + 9]
        src = ipaddress.ip_address(frame[off + 12:off + 16])
        dst = ipaddress.ip_address(frame[off + 16:off + 20])
        total = struct.unpack("!H", frame[off + 2:off + 4])[0]
        l4 = frame[off + ihl:off + total]
    elif etype == 0x86DD:
        proto = frame[off + 6]
        src = ipaddress.ip_address(frame[off + 8:off + 24])
        dst = ipaddress.ip_address(frame[off + 24:off + 40])
        l4 = frame[off + 40:]
    elif etype == 0x0806:
        return ("arp", None, None, None, None, b"")
    else:
        return None
    if proto == 6 and len(l4) >= 20:
        sport, dport = struct.unpack("!HH", l4[:4])
        doff = (l4[12] >> 4) * 4
        return ("tcp", src, sport, dst, dport, l4[doff:])
    if proto == 17 and len(l4) >= 8:
        sport, dport = struct.unpack("!HH", l4[:4])
        return ("udp", src, sport, dst, dport, l4[8:])
    name = {1: "icmp", 58: "icmp6", 2: "igmp"}.get(proto, f"ip{proto}")
    return (name, src, None, dst, None, l4)


def dns_name(msg, off):
    labels, jumps = [], 0
    while off < len(msg) and jumps < 20:
        n = msg[off]
        if n == 0:
            return ".".join(labels), off + 1
        if n & 0xC0 == 0xC0:
            ptr = struct.unpack("!H", msg[off:off + 2])[0] & 0x3FFF
            name, _ = dns_name(msg, ptr)
            labels.append(name)
            return ".".join(labels), off + 2
        labels.append(msg[off + 1:off + 1 + n].decode(errors="replace"))
        off += 1 + n
        jumps += 1
    return ".".join(labels), off


def parse_dns(payload):
    """Devuelve (consultas, respuestas A/AAAA como [(nombre, ip)])."""
    try:
        qd, an = struct.unpack("!HH", payload[4:8])
        off, queries, answers = 12, [], []
        for _ in range(qd):
            name, off = dns_name(payload, off)
            queries.append(name)
            off += 4
        for _ in range(an):
            name, off = dns_name(payload, off)
            rtype, _, _, rdlen = struct.unpack("!HHIH", payload[off:off + 10])
            rdata = payload[off + 10:off + 10 + rdlen]
            if rtype in (1, 28):
                answers.append((name, str(ipaddress.ip_address(rdata))))
            off += 10 + rdlen
        return queries, answers
    except (struct.error, ValueError, IndexError):
        return [], []


def is_public(ip):
    return ip is not None and ip.is_global and not ip.is_multicast


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("pcap")
    ap.add_argument("--host", help="mostrar solo flujos con esta IP")
    ap.add_argument("--dump-tcp", nargs=2, metavar=("IP:PUERTO", "SALIDA"))
    args = ap.parse_args()

    flows = collections.OrderedDict()
    names = {}
    queries = collections.Counter()
    dump = []
    dump_ip, dump_port = None, None
    if args.dump_tcp:
        h, p = args.dump_tcp[0].rsplit(":", 1)
        dump_ip, dump_port = ipaddress.ip_address(h), int(p)

    first = last = None
    for ts, frame in read_pcap(args.pcap):
        first = first or ts
        last = ts
        pkt = parse(frame)
        if not pkt or pkt[0] == "arp":
            continue
        proto, src, sport, dst, dport, payload = pkt
        if proto == "udp" and 53 in (sport, dport):
            qs, ans = parse_dns(payload)
            if dport == 53:
                queries.update(qs)
            for name, ip in ans:
                names.setdefault(ip, name)
        # Clave canónica: el extremo con puerto bien conocido o el de menor puerto es el servidor.
        if sport is not None and dport is not None and sport < dport:
            key = (proto, dst, dport, src, sport)
            direction = "in"
        else:
            key = (proto, src, sport, dst, dport)
            direction = "out"
        fl = flows.setdefault(key, {"pkts": 0, "out": 0, "in": 0, "t0": ts, "t1": ts})
        fl["pkts"] += 1
        fl[direction] += len(payload)
        fl["t1"] = ts
        if dump_ip and proto == "tcp" and payload and (
                (dst == dump_ip and dport == dump_port) or (src == dump_ip and sport == dump_port)):
            arrow = "C->S" if dst == dump_ip else "S->C"
            dump.append((ts, f"{src}:{sport}", arrow, payload))

    print(f"captura: {args.pcap}")
    if first:
        print(f"duración: {last - first:.1f} s")
    print()
    print("Consultas DNS:")
    for q, n in queries.most_common():
        print(f"  {n:5d}  {q}")
    print()
    print(f"{'proto':5} {'cliente':>22}  {'servidor':>22}  {'nombre':30} {'pkts':>6} {'tx':>9} {'rx':>9}  público")
    agg = collections.defaultdict(lambda: {"pkts": 0, "out": 0, "in": 0})
    for (proto, c, cport, s, sport), fl in flows.items():
        if args.host and args.host not in (str(c), str(s)):
            continue
        k = (proto, str(c), str(s), sport)
        for f in ("pkts", "out", "in"):
            agg[k][f] += fl[f]
    for (proto, c, s, sport), fl in sorted(agg.items(), key=lambda kv: -(kv[1]["out"] + kv[1]["in"])):
        sip = ipaddress.ip_address(s)
        name = names.get(s, "")[:30]
        port = f":{sport}" if sport is not None else ""
        print(f"{proto:5} {c:>22}  {s + port:>22}  {name:30} {fl['pkts']:6d} {fl['out']:9d} {fl['in']:9d}  "
              f"{'SÍ' if is_public(sip) else ''}")

    if args.dump_tcp:
        with open(args.dump_tcp[1], "wb") as out:
            for ts, ep, arrow, payload in dump:
                out.write(f"\n### {ts:.6f} {arrow} {ep} len={len(payload)}\n".encode())
                out.write(payload)
        print(f"\n{len(dump)} segmentos TCP escritos en {args.dump_tcp[1]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
