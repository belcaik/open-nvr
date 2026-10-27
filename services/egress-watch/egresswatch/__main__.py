"""python -m egresswatch [run|summary]

Lee los JSON de ulogd2 (grupos NFLOG 1 y 2 de la tabla `inet opennvr`) y el
registro de consultas de dnsmasq, agrega y avisa por ntfy.
"""
import datetime
import os
import sys
import time

from opennvr_common import creds, statefile
from opennvr_common.config import env_float, env_int, env_list, env_str
from opennvr_common.log import log
from opennvr_common.ntfy import Notifier
from opennvr_common.throttle import Throttle

from .aggregate import AggConfig, Aggregator
from .parse import parse_dnsmasq_line, parse_ulog_line
from .tail import FileTail


def load_config():
    agg = AggConfig(
        nvr_ip=env_str("NVR_HOST", "192.168.0.227"),
        window_seconds=env_int("WINDOW_SECONDS", 300),
        spike_factor=env_float("SPIKE_FACTOR", 5.0),
        spike_min_bytes=env_int("SPIKE_MIN_BYTES", 1_000_000),
        summary_hour=env_int("SUMMARY_HOUR", 8),
        silence_hours=env_float("SILENCE_ALERT_HOURS", 6.0),
    )
    return {
        "agg": agg,
        "ulog_files": env_list("ULOG_FILES", ["/var/log/ulogd/nvr-fwd.json",
                                              "/var/log/ulogd/nvr-drop.json"]),
        "dns_file": env_str("DNSMASQ_LOG", "/var/log/dnsmasq/queries.log"),
        "data_dir": env_str("DATA_DIR", "/data"),
        "poll": env_float("POLL_SECONDS", 5.0),
        "log_max_bytes": env_int("LOG_MAX_BYTES", 50_000_000),
        "ntfy_url": env_str("NTFY_URL", ""),
        "ntfy_topic": env_str("NTFY_TOPIC", "opennvr"),
        "ntfy_token": creds.read_secret("NTFY_TOKEN"),
        "min_interval": env_int("NOTIFY_MIN_INTERVAL", 3600),
        "max_per_hour": env_int("NOTIFY_MAX_PER_HOUR", 20),
    }


class EgressWatch:
    def __init__(self, cfg, clock=time.time):
        self.cfg = cfg
        self.clock = clock
        os.makedirs(cfg["data_dir"], exist_ok=True)
        self.state_path = os.path.join(cfg["data_dir"], "state.json")
        self.state = statefile.load(self.state_path)
        now = clock()
        today = datetime.datetime.now().date().isoformat()
        self.agg = Aggregator(self.state.setdefault("agg", {}), cfg["agg"], now, today)
        throttle = Throttle(self.state.setdefault("throttle", {}),
                            cfg["min_interval"], cfg["max_per_hour"])
        self.notifier = Notifier(cfg["ntfy_url"], cfg["ntfy_topic"], cfg["ntfy_token"],
                                 throttle, prefix="NVR tráfico: ")
        tails = self.state.setdefault("tails", {})
        self.ulog_tails = [FileTail(p, tails.setdefault(p, {})) for p in cfg["ulog_files"]]
        self.dns_tail = FileTail(cfg["dns_file"], tails.setdefault(cfg["dns_file"], {}))
        self.last_save = 0.0

    def poll(self):
        now = self.clock()
        alerts = []
        # DNS primero: así las IPs ya tienen nombre cuando llegan sus paquetes.
        for line in self.dns_tail.read_lines():
            ev = parse_dnsmasq_line(line)
            if ev:
                alerts += self.agg.dns(ev, now)
        for tail in self.ulog_tails:
            for line in tail.read_lines():
                flow = parse_ulog_line(line)
                if flow:
                    alerts += self.agg.flow(flow, now)
        alerts += self.agg.tick(now, datetime.datetime.now())
        for a in alerts:
            self.notifier.send(a.key, a.title, a.message, a.priority, a.tags,
                               interval=a.interval, now=now)
        for tail in self.ulog_tails + [self.dns_tail]:
            try:
                if tail.truncate_if_larger(self.cfg["log_max_bytes"]):
                    log(f"{tail.path} vaciado (superaba LOG_MAX_BYTES)")
            except OSError as exc:
                log(f"no se pudo vaciar {tail.path}: {type(exc).__name__}")
        if alerts or now - self.last_save >= 30:
            self.notifier.throttle.prune(now)
            statefile.save(self.state_path, self.state)
            self.last_save = now

    def run_forever(self):
        log(f"egress-watch: NVR {self.cfg['agg'].nvr_ip}, archivos "
            f"{', '.join(self.cfg['ulog_files'] + [self.cfg['dns_file']])}")
        while True:
            try:
                self.poll()
            except Exception as exc:  # el bucle no debe morir por un fallo puntual
                log(f"error en el ciclo: {type(exc).__name__}: {exc}")
            time.sleep(self.cfg["poll"])


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    cmd = argv[0] if argv else "run"
    watch = EgressWatch(load_config())
    if cmd == "run":
        watch.run_forever()
        return 0
    if cmd == "summary":
        # Resumen del día en curso sin reiniciar los contadores.
        print(watch.agg.format_summary(watch.state["agg"]["daily"]))
        return 0
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main())
