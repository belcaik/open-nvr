"""Máquina de estados de disponibilidad por comprobación (lógica pura).

Una comprobación pasa a "caída" tras `threshold` fallos seguidos y vuelve a
"activa" con el primer éxito. Solo se emite un evento en cada transición, así
que una caída larga produce un aviso al empezar y otro al recuperarse.
"""
from collections import namedtuple

Event = namedtuple("Event", "kind name detail duration")

DOWN, UP = "down", "up"


class Availability:
    def __init__(self, state, threshold=3):
        self.state = state
        self.threshold = max(1, threshold)

    def update(self, name, ok, now, detail="", threshold=None):
        threshold = self.threshold if threshold is None else max(1, threshold)
        st = self.state.setdefault(name, {"fails": 0, "down": False, "since": None})
        if ok:
            was_down = st["down"]
            since = st["since"]
            st.update(fails=0, down=False, since=None)
            if was_down:
                return Event(UP, name, detail, now - since if since is not None else None)
            return None
        st["fails"] += 1
        if st["since"] is None:
            st["since"] = now
        if not st["down"] and st["fails"] >= threshold:
            st["down"] = True
            return Event(DOWN, name, detail, None)
        return None

    def down_names(self):
        return sorted(n for n, st in self.state.items() if st.get("down"))


def format_duration(seconds):
    if seconds is None:
        return "?"
    seconds = int(seconds)
    h, rem = divmod(seconds, 3600)
    m, s = divmod(rem, 60)
    if h:
        return f"{h} h {m} min"
    if m:
        return f"{m} min {s} s"
    return f"{s} s"
