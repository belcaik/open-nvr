"""Convierte las respuestas XML de la API del NVR en diccionarios planos.

Las claves son rutas con puntos (`camera.1.IP`). Los nodos repetidos llevan
índice (`Interface[0]`). Se descartan los namespaces.

Los campos sensibles nunca llegan a la línea base:
- usuario y contraseña de cámaras/usuarios se eliminan;
- identificadores (UUID P2P, número de serie, DID) se guardan como hash
  corto, suficiente para detectar que han cambiado sin almacenarlos.
"""
import hashlib
import xml.etree.ElementTree as ET
from collections import Counter

DROP_FIELDS = {"username", "password", "senderemailpassword", "userpassword"}
HASH_FIELDS = {"uuid", "serialnum", "serialnumber", "did", "p2pid"}


def localname(tag):
    return tag.rsplit("}", 1)[-1]


def short_hash(value):
    return "sha256:" + hashlib.sha256(value.encode()).hexdigest()[:12]


def parse(data):
    """Devuelve el elemento raíz. Lanza ValueError si el XML no es válido."""
    if isinstance(data, str):
        data = data.encode()
    try:
        return ET.fromstring(data)
    except ET.ParseError as exc:
        raise ValueError(f"XML no válido: {exc}") from None


def _field(path):
    last = path.rsplit(".", 1)[-1]
    return last.split("[", 1)[0].lower()


def flatten(elem, prefix=""):
    """Aplana un elemento en {ruta: texto}, aplicando las reglas de sensibilidad."""
    out = {}
    _flatten(elem, prefix, out)
    return out


def _flatten(elem, prefix, out):
    children = list(elem)
    if not children:
        if not prefix:
            return
        field = _field(prefix)
        value = (elem.text or "").strip()
        if field in DROP_FIELDS:
            return
        if field in HASH_FIELDS and value:
            value = short_hash(value)
        out[prefix] = value
        return
    counts = Counter(localname(c.tag) for c in children)
    seen = Counter()
    for child in children:
        name = localname(child.tag)
        if counts[name] > 1:
            name = f"{name}[{seen[name]}]"
            seen[localname(child.tag)] += 1
        _flatten(child, f"{prefix}.{name}" if prefix else name, out)


def records(root, tag):
    """Todos los elementos con ese nombre local, a cualquier profundidad."""
    return [e for e in root.iter() if localname(e.tag) == tag]


def _record_key(elem, index, key_fields):
    for field in key_fields:
        for child in elem:
            if localname(child.tag) == field and (child.text or "").strip():
                return child.text.strip()
    return str(index + 1)


def keyed_records(root, tag, section, key_fields=("ChannelID", "ID")):
    """Aplana cada registro `tag` bajo `section.<clave>.`"""
    out = {}
    for i, rec in enumerate(records(root, tag)):
        key = _record_key(rec, i, key_fields)
        out.update(flatten(rec, f"{section}.{key}"))
    return out


# --- Extractores por endpoint -------------------------------------------------

def device_info(data):
    """GET /System/DeviceInfo -> device.*"""
    return flatten(parse(data), "device")


def cameras(data):
    """GET /System/FrontConnectDevicetList -> camera.<ChannelID>.*

    Omite canales vacíos (sin IP ni MAC).
    """
    root = parse(data)
    out = {}
    for i, rec in enumerate(records(root, "DevConnect")):
        flat = flatten(rec, "x")
        ip = flat.get("x.IP", "")
        mac = flat.get("x.MAC", "")
        if ip in ("", "0.0.0.0") and mac in ("", "00:00:00:00:00:00"):
            continue
        key = _record_key(rec, i, ("ChannelID",))
        out.update({f"camera.{key}." + k[2:]: v for k, v in flat.items()})
    return out


def camera_status(data):
    """GET /System/FrontConnectDeviceStatusList -> camstatus.<n>.*"""
    return keyed_records(parse(data), "DevStatus", "camstatus")


def p2p(data):
    """GET /Network/P2PV2 -> p2p.<n>.* (UUID con hash)"""
    return keyed_records(parse(data), "P2P", "p2p", key_fields=())


def ntp(data):
    """GET /System/NTP -> ntp.*"""
    return flatten(parse(data), "ntp")


def network(data):
    """GET /Network/Interfaces -> net.<ID>.* (IP, puerta de enlace, DNS, MAC)"""
    return keyed_records(parse(data), "Interface", "net", key_fields=("ID",))


def device_status(data):
    """GET /System/DeviceStatus -> status.* (bitrates; volátil)"""
    return flatten(parse(data), "status")


def first_value(values, suffix):
    """Primer valor cuya clave termina en `suffix` (p. ej. '.Gateway.IPAddress')."""
    for key in sorted(values):
        if key.endswith(suffix):
            return values[key]
    return None


def values_with_suffix(values, suffix):
    return [values[k] for k in sorted(values) if k.endswith(suffix)]
