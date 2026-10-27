"""Agregación del tráfico del NVR y generación de avisos (lógica pura).

Clave de agregación: (acción, sentido, IP remota, puerto, protocolo), más el
dominio que dnsmasq resolvió para esa IP, si lo hay. Las respuestas a un flujo
permitido (fwd-in) se suman al flujo de salida que las originó.

Avisos:
- destino nuevo (permitido o bloqueado) o dominio nuevo consultado, agrupados
  por ciclo;
- pico de tráfico permitido: una ventana supera SPIKE_MIN_BYTES y
  SPIKE_FACTOR veces la media móvil (EWMA) de las ventanas anteriores;
- silencio: ningún paquete del NVR registrado en SILENCE_HOURS (con el NVR
  detrás de Baphomet siempre hay intentos bloqueados; el silencio indica que
  el NVR ya no usa Baphomet o que el registro está roto);
- resumen diario a SUMMARY_HOUR (hora local).
"""
import hashlib
import ipaddress
from collections import namedtuple
from dataclasses import dataclass

Alert = namedtuple("Alert", "key title message priority tags interval")

MAX_KNOWN = 20000
MAX_DAILY_KEYS = 5000
MAX_DNSMAP = 5000
MAX_LISTED = 30


@dataclass
class AggConfig:
    nvr_ip: str = "192.168.0.227"
    window_seconds: int = 300
    spike_factor: float = 5.0
    spike_min_bytes: int = 1_000_000
    ewma_alpha: float = 0.2
    summary_hour: int = 8
    silence_hours: float = 6.0


def _is_ip(value):
    try:
        ipaddress.ip_address(value)
        return True
    except ValueError:
        return False


def _human(n):
    for unit in ("B", "KiB", "MiB", "GiB"):
        if n < 1024 or unit == "GiB":
            return f"{n:.0f} {unit}" if unit == "B" else f"{n:.1f} {unit}"
        n /= 1024
    return str(n)


def flow_key(flow):
    """Clave de texto 'acción|sentido|ip|puerto|proto' de un paquete."""
    if flow.direction == "in":
        if flow.action == "fwd":     # respuesta a un flujo permitido del NVR
            return f"fwd|out|{flow.src}|{flow.sport}|{flow.proto}"
        return f"drop|in|{flow.src}|{flow.dport}|{flow.proto}"
    return f"{flow.action}|{flow.direction}|{flow.dst}|{flow.dport}|{flow.proto}"


class Aggregator:
    def __init__(self, state, cfg, now, today):
        self.state = state
        self.cfg = cfg
        state.setdefault("known", {})
        state.setdefault("pending_new", [])
        state.setdefault("dnsmap", {})
        state.setdefault("window", {"start": now, "bytes": 0, "packets": 0, "by_key": {}})
        state.setdefault("ewma", None)
        state.setdefault("daily", self._empty_daily())
        state.setdefault("last_event", now)
        state.setdefault("silence_alerted", False)
        state.setdefault("last_summary_date", today)

    @staticmethod
    def _empty_daily():
        return {"flows": {}, "dns": {}, "dns_nx": {}, "new": 0,
                "fwd_packets": 0, "fwd_bytes": 0, "drop_packets": 0}

    # --- entrada --------------------------------------------------------------

    def _remember(self, key, now):
        known = self.state["known"]
        if key in known:
            return
        if len(known) >= MAX_KNOWN:
            del known[next(iter(known))]
        known[key] = now
        self.state["pending_new"].append(key)
        self.state["daily"]["new"] += 1

    def flow(self, flow, now):
        alerts = []
        self.state["last_event"] = now
        if self.state["silence_alerted"]:
            self.state["silence_alerted"] = False
            alerts.append(Alert("silence:end", "vuelve a haber tráfico del NVR registrado",
                                "egress-watch vuelve a recibir paquetes del NVR.", 2,
                                ("white_check_mark",), 0))
        key = flow_key(flow)
        self._remember(key, now)
        daily = self.state["daily"]
        if key in daily["flows"] or len(daily["flows"]) < MAX_DAILY_KEYS:
            entry = daily["flows"].setdefault(key, [0, 0])
            entry[0] += 1
            entry[1] += flow.length
        if flow.action == "fwd":
            daily["fwd_packets"] += 1
            daily["fwd_bytes"] += flow.length
            w = self.state["window"]
            w["bytes"] += flow.length
            w["packets"] += 1
            w["by_key"][key] = w["by_key"].get(key, 0) + flow.length
        else:
            daily["drop_packets"] += 1
        return alerts

    def dns(self, ev, now):
        if ev.kind == "query":
            if self.cfg.nvr_ip and ev.client and ev.client != self.cfg.nvr_ip:
                return []
            daily = self.state["daily"]["dns"]
            if ev.name in daily or len(daily) < MAX_DAILY_KEYS:
                daily[ev.name] = daily.get(ev.name, 0) + 1
            self._remember(f"dns|{ev.name}", now)
        elif ev.kind in ("reply", "cached") and ev.value and _is_ip(ev.value):
            dnsmap = self.state["dnsmap"]
            dnsmap.pop(ev.value, None)
            if len(dnsmap) >= MAX_DNSMAP:
                del dnsmap[next(iter(dnsmap))]
            dnsmap[ev.value] = ev.name
        elif ev.kind == "config" and ev.value == "NXDOMAIN":
            nx = self.state["daily"]["dns_nx"]
            if ev.name in nx or len(nx) < MAX_DAILY_KEYS:
                nx[ev.name] = nx.get(ev.name, 0) + 1
        return []

    # --- presentación ---------------------------------------------------------

    def describe(self, key):
        parts = key.split("|")
        if parts[0] == "dns":
            return f"DNS consulta {parts[1]}"
        action, direction, ip, port, proto = parts
        domain = self.state["dnsmap"].get(ip)
        where = f"{ip}:{port}/{proto}" + (f" ({domain})" if domain else "")
        if direction == "in":
            return f"bloqueado ENTRANTE desde {where}"
        if direction == "v6":
            return f"bloqueado IPv6 hacia {where}"
        if direction == "spoof":
            return f"bloqueado, MAC del NVR con otra IP, hacia {where}"
        return ("PERMITIDO" if action == "fwd" else "bloqueado") + f" hacia {where}"

    # --- reloj ----------------------------------------------------------------

    def tick(self, now, local_now):
        alerts = []
        alerts += self._new_destinations()
        alerts += self._close_windows(now)
        alerts += self._silence(now)
        alerts += self._summary(local_now)
        return alerts

    def _new_destinations(self):
        pending = self.state["pending_new"]
        if not pending:
            return []
        self.state["pending_new"] = []
        lines = [self.describe(k) for k in pending[:MAX_LISTED]]
        if len(pending) > MAX_LISTED:
            lines.append(f"... y {len(pending) - MAX_LISTED} más")
        allowed = any(k.startswith("fwd|") for k in pending)
        digest = hashlib.sha256("\n".join(sorted(pending)).encode()).hexdigest()[:16]
        return [Alert(f"new:{digest}", f"{len(pending)} destinos nuevos del NVR",
                      "\n".join(lines), 4 if allowed else 3, ("mag",), None)]

    def _close_windows(self, now):
        alerts = []
        size = self.cfg.window_seconds
        w = self.state["window"]
        closed = 0
        while now >= w["start"] + size:
            ewma = self.state["ewma"]
            b = w["bytes"]
            if b >= self.cfg.spike_min_bytes and (ewma is None or b > self.cfg.spike_factor * ewma):
                top = sorted(w["by_key"].items(), key=lambda kv: kv[1], reverse=True)[:5]
                lines = [f"{_human(v)}  {self.describe(k)}" for k, v in top]
                avg = "sin historial" if ewma is None else _human(ewma)
                alerts.append(Alert(
                    "spike", f"pico de tráfico permitido: {_human(b)} en {size // 60} min",
                    f"{w['packets']} paquetes; media móvil {avg}.\n" + "\n".join(lines),
                    4, ("chart_with_upwards_trend",), 3600))
            a = self.cfg.ewma_alpha
            self.state["ewma"] = b if ewma is None else a * b + (1 - a) * ewma
            closed += 1
            if closed >= 288:  # tras una parada larga no se recorren todas las ventanas
                w = {"start": now, "bytes": 0, "packets": 0, "by_key": {}}
                break
            w = {"start": w["start"] + size, "bytes": 0, "packets": 0, "by_key": {}}
        self.state["window"] = w
        return alerts

    def _silence(self, now):
        hours = self.cfg.silence_hours
        if hours <= 0 or self.state["silence_alerted"]:
            return []
        idle = now - self.state["last_event"]
        if idle < hours * 3600:
            return []
        self.state["silence_alerted"] = True
        return [Alert("silence", f"sin tráfico del NVR registrado en {idle / 3600:.1f} h",
                      "Posibles causas: el NVR ya no usa Baphomet como puerta de enlace "
                      "(¿DHCP, reinicio, cambio de configuración?), la tabla nft "
                      "`inet opennvr` no está cargada o ulogd2 no está registrando.",
                      4, ("warning",), None)]

    def _summary(self, local_now):
        today = local_now.date().isoformat()
        if local_now.hour < self.cfg.summary_hour or self.state["last_summary_date"] == today:
            return []
        daily = self.state["daily"]
        self.state["daily"] = self._empty_daily()
        self.state["last_summary_date"] = today
        return [Alert(f"summary:{today}", "resumen diario de tráfico del NVR",
                      self.format_summary(daily), 2, ("bar_chart",), None)]

    def format_summary(self, daily):
        lines = [
            f"Permitido: {daily['fwd_packets']} paquetes, {_human(daily['fwd_bytes'])}",
            f"Bloqueado: {daily['drop_packets']} paquetes",
            f"Destinos/dominios nuevos: {daily['new']}",
        ]
        flows = daily["flows"]
        fwd = sorted(((k, v) for k, v in flows.items() if k.startswith("fwd|")),
                     key=lambda kv: kv[1][1], reverse=True)[:10]
        drop = sorted(((k, v) for k, v in flows.items() if not k.startswith("fwd|")),
                      key=lambda kv: kv[1][0], reverse=True)[:10]
        if fwd:
            lines.append("\nPermitidos (por bytes):")
            lines += [f"  {_human(v[1])}  {v[0]} pq  {self.describe(k)}" for k, v in fwd]
        if drop:
            lines.append("\nBloqueados (por paquetes):")
            lines += [f"  {v[0]} pq  {self.describe(k)}" for k, v in drop]
        dns = sorted(daily["dns"].items(), key=lambda kv: kv[1], reverse=True)[:10]
        if dns:
            lines.append("\nDNS más consultados:")
            lines += [f"  {n}  {name}" + ("  (NXDOMAIN)" if name in daily["dns_nx"] else "")
                      for name, n in dns]
        return "\n".join(lines)
