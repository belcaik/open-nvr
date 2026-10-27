#!/usr/bin/env python3
"""Cambia la puerta de enlace del NVR (y opcionalmente su DNS) con copia y vuelta atrás.

Hace lo mismo que la pantalla de red de la UI web (nvr/js/network/ipAddress.js):
GET /Network/Interfaces, modificar los nodos del XML devuelto y PUT del mismo
documento. Desactiva DHCP para que el router no restaure su puerta de enlace al
renovar la concesión. IP, máscara y MTU se conservan.

Credenciales por entorno: NVR_HOST, NVR_USER, NVR_PASS.

    tools/nvr_gateway.py show
    tools/nvr_gateway.py plan  --gateway 192.168.0.8 [--dns 192.168.0.8]
    tools/nvr_gateway.py apply --gateway 192.168.0.8 [--dns 192.168.0.8]
    tools/nvr_gateway.py rollback            # restaura la última copia

Las copias se guardan en captures/network/ (fuera de git).
"""
import argparse
import base64
import datetime
import os
import pathlib
import sys
import time
import urllib.request
import xml.etree.ElementTree as ET

BACKUPS = pathlib.Path(__file__).resolve().parent.parent / "captures" / "network"
FIELDS = ("ipAddress", "SubnetMask", "Gateway", "PrimaryDNS", "SecondaryDNS")


def call(method, path, body=None):
    host = os.environ["NVR_HOST"]
    token = base64.b64encode(f"{os.environ['NVR_USER']}:{os.environ['NVR_PASS']}".encode()).decode()
    req = urllib.request.Request(f"http://{host}{path}", data=body, method=method,
                                 headers={"Authorization": f"Basic {token}", "If-Modified-Since": "0"})
    with urllib.request.urlopen(req, timeout=20) as resp:
        return resp.read()


def find(root, tag):
    for el in root.iter():
        if el.tag.rsplit("}", 1)[-1] == tag:
            return el
    raise KeyError(tag)


def summary(xml_bytes):
    root = ET.fromstring(xml_bytes)
    out = {f: find(root, f).text for f in FIELDS}
    out["DHCP"] = find(find(root, "DHCP"), "Enable").text
    return out


def edit(xml_bytes, gateway, dns):
    root = ET.fromstring(xml_bytes)
    find(root, "Gateway").text = gateway
    find(find(root, "DHCP"), "Enable").text = "false"
    if dns:
        find(root, "PrimaryDNS").text = dns
        find(root, "SecondaryDNS").text = dns
    return ET.tostring(root, encoding="UTF-8", xml_declaration=True)


def put(xml_bytes):
    resp = call("PUT", "/Network/Interfaces", xml_bytes)
    code = find(ET.fromstring(resp), "statusCode").text
    if code != "0":
        raise SystemExit(f"el NVR rechazó el cambio: statusCode={code}")


def wait_and_show():
    for _ in range(12):
        time.sleep(5)
        try:
            print("estado actual:", summary(call("GET", "/Network/Interfaces")))
            return
        except OSError:
            pass
    raise SystemExit("el NVR no responde tras el cambio; ver la vuelta atrás en deploy/baphomet/README.md")


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("action", choices=("show", "plan", "apply", "rollback"))
    ap.add_argument("--gateway")
    ap.add_argument("--dns", help="DNS primario y secundario (opcional)")
    args = ap.parse_args()

    if args.action == "show":
        print(summary(call("GET", "/Network/Interfaces")))
        return 0

    if args.action == "rollback":
        backups = sorted(BACKUPS.glob("interfaces-*.xml"))
        if not backups:
            raise SystemExit("no hay copias en " + str(BACKUPS))
        original = backups[-1].read_bytes()
        print("restaurando", backups[-1].name, summary(original))
        put(original)
        wait_and_show()
        return 0

    if not args.gateway:
        ap.error("--gateway es obligatorio para plan/apply")
    current = call("GET", "/Network/Interfaces")
    new = edit(current, args.gateway, args.dns)
    print("antes:  ", summary(current))
    print("después:", summary(new))
    if args.action == "plan":
        return 0

    BACKUPS.mkdir(parents=True, exist_ok=True)
    stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    (BACKUPS / f"interfaces-{stamp}.xml").write_bytes(current)
    put(new)
    wait_and_show()
    return 0


if __name__ == "__main__":
    sys.exit(main())
