"""Configuración de nvr-watch desde el entorno (.env + podman secrets)."""
from dataclasses import dataclass, field

from opennvr_common import creds
from opennvr_common.config import env_bool, env_int, env_list, env_ports, env_str

from .ports import KNOWN_OPEN

DEFAULT_IGNORE = (
    "status.",                      # bitrates instantáneos
    "camstatus.",                   # se vigila como disponibilidad (camera:N)
    "ntp.LastSuccessSyncNTPTime",
    "ntp.IsSynchronizeNTPNow",
    "ntp.SynchronizeNTPNowStatus",
)


@dataclass
class Config:
    host: str = "192.168.0.227"
    http_port: int = 80
    rtsp_port: int = 554
    rtsp_path: str = "/live/0/SUB"
    user: str = ""
    password: str = field(default="", repr=False)
    known_ports: list = field(default_factory=lambda: list(KNOWN_OPEN))
    scan_extra: list = field(default_factory=list)
    scan_enabled: bool = True
    check_interval: int = 60
    baseline_interval: int = 900
    scan_interval: int = 21600
    fail_threshold: int = 3
    time_drift_max: int = 120
    expected_gateway: str = ""
    expect_p2p_offline: bool = False
    p2p_online_states: list = field(default_factory=lambda: ["internet-ready"])
    ignore_prefixes: list = field(default_factory=lambda: list(DEFAULT_IGNORE))
    data_dir: str = "/data"
    ntfy_url: str = ""
    ntfy_topic: str = "opennvr"
    ntfy_token: str = field(default="", repr=False)
    notify_min_interval: int = 3600
    notify_max_per_hour: int = 20
    baseline_realert: int = 86400


def from_env(environ=None):
    e = environ
    user = env_str("NVR_USER", "", e)
    password = creds.read_secret("NVR_PASS", environ=e)
    creds.register_basic(user, password)
    return Config(
        host=env_str("NVR_HOST", "192.168.0.227", e),
        http_port=env_int("NVR_HTTP_PORT", 80, e),
        rtsp_port=env_int("NVR_RTSP_PORT", 554, e),
        rtsp_path=env_str("NVR_RTSP_PATH", "/live/0/SUB", e),
        user=user,
        password=password,
        known_ports=env_ports("NVR_KNOWN_TCP_PORTS", KNOWN_OPEN, e),
        scan_extra=env_ports("SCAN_EXTRA_PORTS", (), e),
        scan_enabled=env_bool("SCAN_ENABLED", True, e),
        check_interval=env_int("CHECK_INTERVAL", 60, e),
        baseline_interval=env_int("BASELINE_INTERVAL", 900, e),
        scan_interval=env_int("SCAN_INTERVAL", 21600, e),
        fail_threshold=env_int("FAIL_THRESHOLD", 3, e),
        time_drift_max=env_int("TIME_DRIFT_MAX", 120, e),
        expected_gateway=env_str("EXPECTED_GATEWAY", "", e),
        expect_p2p_offline=env_bool("EXPECT_P2P_OFFLINE", False, e),
        p2p_online_states=[s.lower() for s in env_list("P2P_ONLINE_STATES", ["internet-ready"], e)],
        ignore_prefixes=env_list("IGNORE_PREFIXES", DEFAULT_IGNORE, e),
        data_dir=env_str("DATA_DIR", "/data", e),
        ntfy_url=env_str("NTFY_URL", "", e),
        ntfy_topic=env_str("NTFY_TOPIC", "opennvr", e),
        ntfy_token=creds.read_secret("NTFY_TOKEN", environ=e),
        notify_min_interval=env_int("NOTIFY_MIN_INTERVAL", 3600, e),
        notify_max_per_hour=env_int("NOTIFY_MAX_PER_HOUR", 20, e),
        baseline_realert=env_int("BASELINE_REALERT", 86400, e),
    )
