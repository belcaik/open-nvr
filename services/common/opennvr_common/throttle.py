"""Deduplicación y límite de frecuencia de avisos.

Lógica pura: el tiempo llega como argumento y el estado es un dict que el
llamador persiste. Dos reglas:

1. Deduplicación: la misma clave no se envía dos veces antes de
   `min_interval` segundos (o del intervalo propio que se pase en `allow`).
2. Límite global: como mucho `max_per_hour` avisos en cualquier ventana de
   una hora.

Los avisos descartados se cuentan; el siguiente aviso enviado lo menciona.
"""


class Throttle:
    def __init__(self, state, min_interval=3600, max_per_hour=20):
        self.state = state
        self.min_interval = min_interval
        self.max_per_hour = max_per_hour
        state.setdefault("last", {})
        state.setdefault("sent", [])
        state.setdefault("suppressed", 0)

    def allow(self, key, now, interval=None):
        interval = self.min_interval if interval is None else interval
        last = self.state["last"].get(key)
        if last is not None and now - last < interval:
            self.state["suppressed"] += 1
            return False
        recent = [t for t in self.state["sent"] if now - t < 3600]
        if len(recent) >= self.max_per_hour:
            self.state["sent"] = recent
            self.state["suppressed"] += 1
            return False
        recent.append(now)
        self.state["sent"] = recent
        self.state["last"][key] = now
        return True

    def forget(self, key, now):
        """Deshace el registro de `allow` cuando el envío ha fallado."""
        self.state["last"].pop(key, None)
        if now in self.state["sent"]:
            self.state["sent"].remove(now)

    def take_suppressed(self):
        count = self.state["suppressed"]
        self.state["suppressed"] = 0
        return count

    def prune(self, now, max_age=7 * 86400):
        """Olvida claves antiguas para que el estado no crezca sin límite."""
        last = self.state["last"]
        for key in [k for k, t in last.items() if now - t > max_age]:
            del last[key]
