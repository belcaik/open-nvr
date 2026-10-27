"""Sondas de red de solo lectura contra el NVR (clase L del plan).

Una petición cada vez, con timeout, sin paralelismo. Las credenciales solo
viajan en cabeceras y nunca forman parte de mensajes de error.
"""
import base64
import hashlib
import http.client
import re
import socket
import time
from email.utils import parsedate_to_datetime

MAX_BODY = 1 << 20


def tcp_connect(host, port, timeout=3.0):
    """(ok, detalle) de un connect TCP completo, cerrado de inmediato."""
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True, "abierto"
    except socket.timeout:
        return False, "timeout"
    except ConnectionRefusedError:
        return False, "rechazado"
    except OSError as exc:
        return False, type(exc).__name__


def scan(host, ports, timeout=1.0, delay=0.2, sleep=time.sleep):
    """Escaneo connect secuencial y lento. Devuelve el conjunto de puertos abiertos."""
    open_ports = set()
    for port in ports:
        ok, _ = tcp_connect(host, port, timeout)
        if ok:
            open_ports.add(port)
        sleep(delay)
    return open_ports


def basic_auth(user, password):
    return "Basic " + base64.b64encode(f"{user}:{password}".encode()).decode()


def http_get(host, port, path, user, password, timeout=10.0):
    """GET con HTTP Basic. Devuelve (status, cabeceras en minúsculas, cuerpo)."""
    conn = http.client.HTTPConnection(host, port, timeout=timeout)
    try:
        headers = {"User-Agent": "open-nvr-watch/1", "Connection": "close"}
        if user or password:
            headers["Authorization"] = basic_auth(user, password)
        conn.request("GET", path, headers=headers)
        resp = conn.getresponse()
        body = resp.read(MAX_BODY)
        return resp.status, {k.lower(): v for k, v in resp.getheaders()}, body
    finally:
        conn.close()


def http_date_drift(headers, now):
    """Segundos que el reloj del NVR va por delante (+) o por detrás (-).

    Usa la cabecera Date de la respuesta HTTP (GMT según la RFC). Devuelve
    None si no hay cabecera o no se puede interpretar.
    """
    value = headers.get("date")
    if not value:
        return None
    try:
        return parsedate_to_datetime(value).timestamp() - now
    except (TypeError, ValueError, IndexError):
        return None


def _rtsp_request(sock, url, cseq, extra):
    lines = [f"DESCRIBE {url} RTSP/1.0", f"CSeq: {cseq}", "Accept: application/sdp",
             "User-Agent: open-nvr-watch/1"]
    lines += [f"{k}: {v}" for k, v in extra.items()]
    sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode())
    data = b""
    while b"\r\n\r\n" not in data and len(data) < 65536:
        chunk = sock.recv(4096)
        if not chunk:
            break
        data += chunk
    head, _, body = data.partition(b"\r\n\r\n")
    head = head.decode(errors="replace")
    m = re.search(r"(?im)^content-length:\s*(\d+)", head)
    length = min(int(m.group(1)), 65536) if m else 0
    while len(body) < length:
        chunk = sock.recv(4096)
        if not chunk:
            break
        body += chunk
    return head


def _status(head):
    parts = head.split(" ", 2)
    if len(parts) >= 2 and parts[0].startswith("RTSP/") and parts[1].isdigit():
        return int(parts[1])
    return None


def _rtsp_auth(head, user, password, url):
    digest = re.search(r"(?i)WWW-Authenticate:\s*Digest\s+(.*)", head)
    if digest:
        params = dict(re.findall(r'(\w+)="([^"]*)"', digest.group(1)))
        ha1 = hashlib.md5(f"{user}:{params.get('realm', '')}:{password}".encode()).hexdigest()
        ha2 = hashlib.md5(f"DESCRIBE:{url}".encode()).hexdigest()
        resp = hashlib.md5(f"{ha1}:{params.get('nonce', '')}:{ha2}".encode()).hexdigest()
        return (f'Digest username="{user}", realm="{params.get("realm", "")}", '
                f'nonce="{params.get("nonce", "")}", uri="{url}", response="{resp}"')
    return basic_auth(user, password)


def rtsp_describe(host, port, path, user, password, timeout=5.0):
    """DESCRIBE (sin SETUP ni PLAY). Devuelve (ok, detalle).

    Con credenciales exige 200; sin ellas basta con una respuesta RTSP
    (normalmente 401), que prueba que el servicio contesta.
    """
    url = f"rtsp://{host}:{port}{path}"
    try:
        with socket.create_connection((host, port), timeout=timeout) as sock:
            head = _rtsp_request(sock, url, 1, {})
            status = _status(head)
            if status == 401 and (user or password):
                auth = _rtsp_auth(head, user, password, url)
                head = _rtsp_request(sock, url, 2, {"Authorization": auth})
                status = _status(head)
    except socket.timeout:
        return False, "timeout"
    except OSError as exc:
        return False, type(exc).__name__
    if status is None:
        return False, "respuesta no RTSP"
    if user or password:
        return status == 200, f"RTSP {status}"
    return True, f"RTSP {status}"
