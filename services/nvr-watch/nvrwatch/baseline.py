"""Comparación de una instantánea del NVR con la línea base (lógica pura).

Una instantánea es un dict plano {clave: texto}. La sección de una clave es
lo que va antes del primer punto (`device`, `camera`, `p2p`, `port`...).

Solo se comparan las secciones que se obtuvieron en esta ronda: si un
endpoint falla, su sección no aparece y no se generan falsos "eliminado".
La caída del endpoint la avisa la comprobación de disponibilidad.
"""
import hashlib
from collections import namedtuple

Change = namedtuple("Change", "kind key old new")

ADDED, REMOVED, CHANGED = "añadido", "eliminado", "cambiado"


def section(key):
    return key.split(".", 1)[0]


def _ignored(key, ignore_prefixes):
    return any(key == p or key.startswith(p) for p in ignore_prefixes)


def diff(base, current, sections, ignore_prefixes=()):
    """Lista ordenada de cambios entre `base` y `current` en `sections`."""
    keys = {k for k in set(base) | set(current)
            if section(k) in sections and not _ignored(k, ignore_prefixes)}
    changes = []
    for key in sorted(keys):
        if key not in base:
            changes.append(Change(ADDED, key, None, current[key]))
        elif key not in current:
            changes.append(Change(REMOVED, key, base[key], None))
        elif base[key] != current[key]:
            changes.append(Change(CHANGED, key, base[key], current[key]))
    return changes


def fingerprint(changes):
    """Identificador estable de un conjunto de cambios (para deduplicar)."""
    h = hashlib.sha256()
    for c in sorted(changes):
        h.update(repr(tuple(c)).encode())
    return h.hexdigest()[:16]


def format_changes(changes, limit=25):
    lines = []
    for c in changes[:limit]:
        if c.kind == ADDED:
            lines.append(f"+ {c.key} = {c.new!r}")
        elif c.kind == REMOVED:
            lines.append(f"- {c.key} (era {c.old!r})")
        else:
            lines.append(f"~ {c.key}: {c.old!r} -> {c.new!r}")
    if len(changes) > limit:
        lines.append(f"... y {len(changes) - limit} cambios más")
    return "\n".join(lines)


def merge(base, current, sections):
    """Línea base nueva: las secciones de `current` sustituyen a las de `base`."""
    out = {k: v for k, v in base.items() if section(k) not in sections}
    out.update({k: v for k, v in current.items() if section(k) in sections})
    return out
