#!/usr/bin/env python3
"""Envía un único RTSP DESCRIBE (con autenticación Digest o Basic) y muestra la respuesta.

Solo lectura: no hace SETUP ni PLAY. Sirve para inspeccionar el SDP y comprobar
si el servidor acepta cabeceras como `Require: www.onvif.org/ver20/backchannel`.

Credenciales por entorno: NVR_USER, NVR_PASS.

    tools/rtsp_describe.py rtsp://192.168.0.227:554/live/0/MAIN
    tools/rtsp_describe.py --backchannel rtsp://192.168.0.227:554/live/0/MAIN
"""
import argparse
import base64
import hashlib
import os
import re
import socket
import sys
from urllib.parse import urlsplit


def request(sock, url, cseq, headers):
    lines = [f"DESCRIBE {url} RTSP/1.0", f"CSeq: {cseq}", "Accept: application/sdp"]
    lines += [f"{k}: {v}" for k, v in headers.items()]
    sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode())
    data = b""
    while b"\r\n\r\n" not in data:
        chunk = sock.recv(4096)
        if not chunk:
            break
        data += chunk
    head, _, body = data.partition(b"\r\n\r\n")
    head = head.decode(errors="replace")
    m = re.search(r"(?im)^content-length:\s*(\d+)", head)
    length = int(m.group(1)) if m else 0
    while len(body) < length:
        chunk = sock.recv(4096)
        if not chunk:
            break
        body += chunk
    return head, body.decode(errors="replace")


def auth_header(head, user, password, url):
    digest = re.search(r'(?i)WWW-Authenticate:\s*Digest\s+(.*)', head)
    if digest:
        params = dict(re.findall(r'(\w+)="([^"]*)"', digest.group(1)))
        ha1 = hashlib.md5(f"{user}:{params['realm']}:{password}".encode()).hexdigest()
        ha2 = hashlib.md5(f"DESCRIBE:{url}".encode()).hexdigest()
        resp = hashlib.md5(f"{ha1}:{params['nonce']}:{ha2}".encode()).hexdigest()
        return (f'Digest username="{user}", realm="{params["realm"]}", '
                f'nonce="{params["nonce"]}", uri="{url}", response="{resp}"')
    token = base64.b64encode(f"{user}:{password}".encode()).decode()
    return f"Basic {token}"


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("url", help="URL RTSP sin credenciales")
    ap.add_argument("--backchannel", action="store_true",
                    help="añade Require: www.onvif.org/ver20/backchannel")
    args = ap.parse_args()

    user, password = os.environ.get("NVR_USER", ""), os.environ.get("NVR_PASS", "")
    parts = urlsplit(args.url)
    headers = {"User-Agent": "open-nvr-describe/1"}
    if args.backchannel:
        headers["Require"] = "www.onvif.org/ver20/backchannel"

    with socket.create_connection((parts.hostname, parts.port or 554), timeout=5) as sock:
        head, body = request(sock, args.url, 1, headers)
        if head.split(" ", 2)[1:2] == ["401"]:
            headers["Authorization"] = auth_header(head, user, password, args.url)
            head, body = request(sock, args.url, 2, headers)
    print(head)
    print()
    print(body)
    return 0 if " 200 " in head.splitlines()[0] else 1


if __name__ == "__main__":
    sys.exit(main())
