"""Lectura de configuración desde variables de entorno."""
import os


def env_str(name, default="", environ=None):
    environ = os.environ if environ is None else environ
    return environ.get(name, default).strip()


def env_int(name, default, environ=None):
    value = env_str(name, "", environ)
    return int(value) if value else default


def env_float(name, default, environ=None):
    value = env_str(name, "", environ)
    return float(value) if value else default


def env_bool(name, default=False, environ=None):
    value = env_str(name, "", environ).lower()
    if not value:
        return default
    return value in ("1", "true", "yes", "si", "sí", "on")


def env_list(name, default=(), environ=None):
    value = env_str(name, "", environ)
    if not value:
        return list(default)
    return [item.strip() for item in value.split(",") if item.strip()]


def env_ports(name, default=(), environ=None):
    return sorted({int(p) for p in env_list(name, [str(d) for d in default], environ)})
