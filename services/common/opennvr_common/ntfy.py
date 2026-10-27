"""Envío de avisos a un servidor ntfy existente.

Publica en JSON en la raíz del servidor (`POST {NTFY_URL}` con `topic` en el
cuerpo) para que títulos y mensajes UTF-8 lleguen sin codificar cabeceras.
Sin NTFY_URL funciona en modo prueba: solo escribe el aviso en el registro.
"""
import json
import time
import urllib.request

from .log import log
from .creds import redact

PRIORITY_LOW = 2
PRIORITY_DEFAULT = 3
PRIORITY_HIGH = 4
PRIORITY_URGENT = 5


def build_payload(topic, title, message, priority=PRIORITY_DEFAULT, tags=()):
    return {
        "topic": topic,
        "title": redact(title),
        "message": redact(message),
        "priority": int(priority),
        "tags": list(tags),
    }


def _http_post(url, payload, token, timeout=10):
    req = urllib.request.Request(
        url.rstrip("/") + "/",
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        resp.read()


class Notifier:
    def __init__(self, url, topic, token, throttle, sender=_http_post, prefix=""):
        self.url = url
        self.topic = topic
        self.token = token
        self.throttle = throttle
        self.sender = sender
        self.prefix = prefix

    def send(self, key, title, message, priority=PRIORITY_DEFAULT, tags=(),
             interval=None, now=None):
        now = time.time() if now is None else now
        if not self.throttle.allow(key, now, interval):
            log(f"aviso suprimido (dedup/límite): {title}")
            return False
        suppressed = self.throttle.take_suppressed()
        if suppressed:
            message += f"\n\n({suppressed} avisos anteriores suprimidos por dedup/límite)"
        payload = build_payload(self.topic, self.prefix + title, message, priority, tags)
        if not self.url:
            log(f"[ntfy desactivado] {payload['title']}: {payload['message']}")
            return True
        try:
            self.sender(self.url, payload, self.token)
        except Exception as exc:  # red caída, ntfy caído: no debe parar el bucle
            self.throttle.forget(key, now)
            self.throttle.state["suppressed"] += suppressed
            log(f"error enviando a ntfy: {type(exc).__name__}: {redact(exc)}")
            return False
        log(f"aviso enviado: {payload['title']}")
        return True
