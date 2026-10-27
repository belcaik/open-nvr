"""Comprobaciones derivadas de una instantánea (lógica pura).

Cada comprobación devuelve (nombre, ok, detalle) y pasa por la máquina de
estados de disponibilidad, igual que los puertos o el RTSP.
"""
import re

from .xmlparse import first_value

LABELS = {
    "http:api": "API HTTP (/System/DeviceInfo)",
    "rtsp": "RTSP DESCRIBE",
    "time": "hora del NVR",
    "gateway": "puerta de enlace del NVR",
    "p2p_offline": "P2P aislado de Internet",
}


def label(name):
    if name in LABELS:
        return LABELS[name]
    if name.startswith("tcp:"):
        return f"puerto {name[4:]}/tcp"
    if name.startswith("camera:"):
        return f"cámara del canal {name[7:]}"
    return name


def time_check(drift, max_drift):
    if drift is None:
        return None
    return ("time", abs(drift) <= max_drift, f"deriva {drift:+.0f} s (máx. {max_drift} s)")


def gateway_check(values, expected):
    if not expected:
        return None
    gw = first_value(values, ".Gateway.IPAddress") or first_value(values, ".Gateway")
    if gw is None:
        return None
    return ("gateway", gw == expected, f"puerta de enlace {gw} (esperada {expected})")


def p2p_check(values, enabled, online_states):
    """Con EXPECT_P2P_OFFLINE, falla si algún campo P2P indica conexión a la nube.

    No se sabe qué campo exacto contiene "internet-ready", así que se miran
    todos los valores de la sección p2p.
    """
    if not enabled:
        return None
    p2p = {k: v for k, v in values.items() if k.startswith("p2p.")}
    if not p2p:
        return None
    hits = sorted(f"{k}={v}" for k, v in p2p.items() if v.strip().lower() in online_states)
    if hits:
        return ("p2p_offline", False, "P2P conectado: " + ", ".join(hits))
    return ("p2p_offline", True, "P2P sin conexión a la nube")


_CAMSTATUS = re.compile(r"^camstatus\.([^.]+)\.LoginStatus$")
_CAMERA = re.compile(r"^camera\.([^.]+)\.")


def camera_checks(values):
    """Estado de login de cada canal que tiene una cámara registrada."""
    registered = {m.group(1) for k in values if (m := _CAMERA.match(k))}
    out = []
    for key in sorted(values):
        m = _CAMSTATUS.match(key)
        if m and m.group(1) in registered:
            status = values[key]
            out.append((f"camera:{m.group(1)}", status.strip().lower() == "connect success",
                        f"LoginStatus={status!r}"))
    return out


def behaviour_checks(values, drift, cfg):
    checks = [time_check(drift, cfg.time_drift_max),
              gateway_check(values, cfg.expected_gateway),
              p2p_check(values, cfg.expect_p2p_offline, cfg.p2p_online_states)]
    return [c for c in checks if c] + camera_checks(values)
