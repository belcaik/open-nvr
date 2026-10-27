"""Bucle principal de nvr-watch (sin cron: el intervalo lo lleva el propio proceso).

Archivos en DATA_DIR (volumen):
- baseline.json       línea base aceptada; solo se escribe al crearla o con `accept`.
- last_snapshot.json  última instantánea obtenida.
- state.json          estado interno (disponibilidad, dedup, último escaneo).
"""
import os
import time

from opennvr_common import statefile
from opennvr_common.log import log
from opennvr_common.ntfy import PRIORITY_DEFAULT, PRIORITY_HIGH, PRIORITY_LOW, Notifier
from opennvr_common.throttle import Throttle

from . import baseline, probes, xmlparse
from .availability import DOWN, Availability, format_duration
from .checks import behaviour_checks, label
from .ports import scan_ports

ENDPOINTS = (
    ("device", "/System/DeviceInfo", xmlparse.device_info),
    ("camera", "/System/FrontConnectDevicetList", xmlparse.cameras),
    ("camstatus", "/System/FrontConnectDeviceStatusList", xmlparse.camera_status),
    ("p2p", "/Network/P2PV2", xmlparse.p2p),
    ("ntp", "/System/NTP", xmlparse.ntp),
    ("net", "/Network/Interfaces", xmlparse.network),
    ("status", "/System/DeviceStatus", xmlparse.device_status),
)


class App:
    def __init__(self, cfg, notifier=None, clock=time.time, sleep=time.sleep):
        self.cfg = cfg
        self.clock = clock
        self.sleep = sleep
        os.makedirs(cfg.data_dir, exist_ok=True)
        self.state_path = os.path.join(cfg.data_dir, "state.json")
        self.baseline_path = os.path.join(cfg.data_dir, "baseline.json")
        self.snapshot_path = os.path.join(cfg.data_dir, "last_snapshot.json")
        self.state = statefile.load(self.state_path)
        self.avail = Availability(self.state.setdefault("avail", {}), cfg.fail_threshold)
        self.throttle = Throttle(self.state.setdefault("throttle", {}),
                                 cfg.notify_min_interval, cfg.notify_max_per_hour)
        self.notifier = notifier or Notifier(cfg.ntfy_url, cfg.ntfy_topic, cfg.ntfy_token,
                                             self.throttle, prefix="NVR: ")

    # --- disponibilidad -----------------------------------------------------

    def check_availability(self, now):
        cfg = self.cfg
        results = []
        for port in cfg.known_ports:
            ok, detail = probes.tcp_connect(cfg.host, port)
            results.append((f"tcp:{port}", ok, detail))
        try:
            status, _, _ = probes.http_get(cfg.host, cfg.http_port, "/System/DeviceInfo",
                                           cfg.user, cfg.password)
            detail = f"HTTP {status}" + (" (credenciales rechazadas)" if status == 401 else "")
            results.append(("http:api", status == 200, detail))
        except OSError as exc:
            results.append(("http:api", False, type(exc).__name__))
        ok, detail = probes.rtsp_describe(cfg.host, cfg.rtsp_port, cfg.rtsp_path,
                                          cfg.user, cfg.password)
        results.append(("rtsp", ok, detail))
        return self._apply(results, now)

    def _apply(self, results, now, threshold=None):
        events = []
        for name, ok, detail in results:
            ev = self.avail.update(name, ok, now, detail, threshold)
            if ev:
                events.append(ev)
        return events

    def notify_events(self, events, now):
        downs = [e for e in events if e.kind == DOWN]
        ups = [e for e in events if e.kind != DOWN]
        tcp_names = {f"tcp:{p}" for p in self.cfg.known_ports}
        if downs:
            down_now = set(self.avail.down_names())
            if tcp_names and tcp_names <= down_now:
                title = f"inaccesible ({self.cfg.host})"
            else:
                title = "fallo: " + ", ".join(label(e.name) for e in downs)
            body = "\n".join(f"{label(e.name)}: {e.detail}" for e in downs)
            self.notifier.send("down:" + ",".join(sorted(e.name for e in downs)), title, body,
                               PRIORITY_HIGH, ("rotating_light",), now=now)
        if ups:
            title = "recuperado: " + ", ".join(label(e.name) for e in ups)
            body = "\n".join(f"{label(e.name)}: {e.detail} (caído {format_duration(e.duration)})"
                             for e in ups)
            self.notifier.send("up:" + ",".join(sorted(e.name for e in ups)), title, body,
                               PRIORITY_DEFAULT, ("white_check_mark",), now=now)

    # --- comportamiento -----------------------------------------------------

    def fetch_snapshot(self, now):
        """Devuelve (valores, secciones obtenidas, deriva horaria en s o None)."""
        cfg = self.cfg
        values, sections, drift = {}, set(), None
        for sec, path, extract in ENDPOINTS:
            try:
                status, headers, body = probes.http_get(cfg.host, cfg.http_port, path,
                                                        cfg.user, cfg.password)
            except OSError as exc:
                log(f"{path}: {type(exc).__name__}")
                continue
            if sec == "device":
                drift = probes.http_date_drift(headers, self.clock())
            if status != 200:
                log(f"{path}: HTTP {status}")
                continue
            try:
                values.update(extract(body))
                sections.add(sec)
            except ValueError as exc:
                log(f"{path}: {exc}")
        scan = self.state.get("scan")
        if scan:
            values.update({f"port.{p}": "open" for p in scan["open"]})
            sections.add("port")
        return values, sections, drift

    def check_behaviour(self, now):
        values, sections, drift = self.fetch_snapshot(now)
        statefile.save(self.snapshot_path, {"ts": now, "sections": sorted(sections),
                                            "values": values})
        events = self._apply(behaviour_checks(values, drift, self.cfg), now)
        self.notify_events(events, now)
        if not sections:
            return []
        return self.compare_baseline(values, sections, now)

    def compare_baseline(self, values, sections, now):
        base = statefile.load(self.baseline_path)
        if not base:
            statefile.save(self.baseline_path, {"created": now, "sections": sorted(sections),
                                                "values": values})
            self.notifier.send("baseline:new", "línea base creada",
                               f"{len(values)} claves en {', '.join(sorted(sections))}.",
                               PRIORITY_LOW, ("memo",), now=now)
            return []
        new_sections = sections - set(base.get("sections", []))
        if new_sections:
            base["values"] = baseline.merge(base["values"], values, new_sections)
            base["sections"] = sorted(set(base["sections"]) | new_sections)
            statefile.save(self.baseline_path, base)
            log(f"secciones añadidas a la línea base: {', '.join(sorted(new_sections))}")
        changes = baseline.diff(base["values"], values, sections - new_sections,
                                self.cfg.ignore_prefixes)
        if changes:
            body = (baseline.format_changes(changes)
                    + "\n\nSi el cambio es esperado: podman exec opennvr-nvr-watch "
                      "python -m nvrwatch accept")
            self.notifier.send("baseline:" + baseline.fingerprint(changes),
                               f"{len(changes)} cambios respecto a la línea base", body,
                               PRIORITY_HIGH, ("warning",), interval=self.cfg.baseline_realert,
                               now=now)
        return changes

    # --- escaneo de puertos -------------------------------------------------

    def run_scan(self, now):
        ports = scan_ports(list(self.cfg.known_ports) + list(self.cfg.scan_extra))
        log(f"escaneo TCP connect de {len(ports)} puertos")
        open_ports = probes.scan(self.cfg.host, ports, sleep=self.sleep)
        if not open_ports:
            log("escaneo sin puertos abiertos: NVR inaccesible, no se guarda")
            return
        self.state["scan"] = {"ts": now, "open": sorted(open_ports)}
        log(f"puertos abiertos: {sorted(open_ports)}")

    # --- bucle --------------------------------------------------------------

    def tick(self, now):
        cfg = self.cfg
        events = self.check_availability(now)
        self.notify_events(events, now)
        nvr_up = "http:api" not in self.avail.down_names()
        if cfg.scan_enabled and nvr_up and now - self.state.get("last_scan", 0) >= cfg.scan_interval:
            self.run_scan(now)
            self.state["last_scan"] = now
        if nvr_up and now - self.state.get("last_baseline", 0) >= cfg.baseline_interval:
            self.check_behaviour(now)
            self.state["last_baseline"] = now
        self.throttle.prune(now)
        statefile.save(self.state_path, self.state)

    def run_forever(self):
        log(f"nvr-watch: NVR {self.cfg.host}, intervalo {self.cfg.check_interval} s, "
            f"línea base cada {self.cfg.baseline_interval} s, escaneo cada {self.cfg.scan_interval} s")
        while True:
            start = self.clock()
            try:
                self.tick(start)
            except Exception as exc:  # el bucle no debe morir por un fallo puntual
                log(f"error en el ciclo: {type(exc).__name__}: {exc}")
            self.sleep(max(1.0, self.cfg.check_interval - (self.clock() - start)))


def accept(cfg):
    """Acepta la última instantánea como línea base (tras revisar un cambio)."""
    base_path = os.path.join(cfg.data_dir, "baseline.json")
    snap = statefile.load(os.path.join(cfg.data_dir, "last_snapshot.json"))
    if not snap:
        print("no hay instantánea todavía")
        return 1
    base = statefile.load(base_path) or {"sections": [], "values": {}}
    sections = set(snap["sections"])
    changes = baseline.diff(base["values"], snap["values"], sections, cfg.ignore_prefixes)
    base["values"] = baseline.merge(base["values"], snap["values"], sections)
    base["sections"] = sorted(set(base["sections"]) | sections)
    base["accepted"] = time.time()
    statefile.save(base_path, base)
    print(baseline.format_changes(changes, limit=200) or "sin cambios")
    print(f"línea base actualizada ({len(changes)} cambios aceptados)")
    return 0


def show(cfg):
    """Muestra las diferencias entre la última instantánea y la línea base."""
    base = statefile.load(os.path.join(cfg.data_dir, "baseline.json"))
    snap = statefile.load(os.path.join(cfg.data_dir, "last_snapshot.json"))
    if not base or not snap:
        print("falta línea base o instantánea")
        return 1
    changes = baseline.diff(base["values"], snap["values"], set(snap["sections"]),
                            cfg.ignore_prefixes)
    print(baseline.format_changes(changes, limit=500) or "sin cambios")
    return 0
