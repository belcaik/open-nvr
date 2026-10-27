"""Registro a stdout (lo recoge podman/journald), siempre redactado."""
import sys
import time

from .creds import redact


def log(msg, stream=None):
    stamp = time.strftime("%Y-%m-%dT%H:%M:%S%z")
    print(f"{stamp} {redact(msg)}", file=stream or sys.stdout, flush=True)
