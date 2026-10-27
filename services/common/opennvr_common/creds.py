"""Lectura de secretos y redacción de texto antes de registrarlo o notificarlo.

Los secretos se leen de un archivo (podman secret montado en /run/secrets/...)
o, si no hay archivo, de una variable de entorno. Cada secreto leído se
registra para que `redact()` lo sustituya en cualquier texto de salida.
"""
import base64
import os

_SECRETS = set()


def register(value):
    """Añade un valor a la lista de textos que nunca deben salir en claro."""
    if value and len(value) >= 3:
        _SECRETS.add(value)


def register_basic(user, password):
    """Registra también la cabecera Basic que se construye con usuario y clave."""
    if password:
        register(base64.b64encode(f"{user}:{password}".encode()).decode())


def read_secret(env_name, file_env_name=None, environ=None):
    """Devuelve el secreto de `<NAME>_FILE` (si existe) o de `<NAME>`.

    El valor se registra para redacción. Un archivo vacío equivale a no
    haber secreto (útil para el token opcional de ntfy).
    """
    environ = os.environ if environ is None else environ
    file_env_name = file_env_name or f"{env_name}_FILE"
    path = environ.get(file_env_name, "")
    value = ""
    if path and os.path.exists(path):
        with open(path, encoding="utf-8") as fh:
            value = fh.read().strip()
    elif environ.get(env_name):
        value = environ[env_name].strip()
    register(value)
    return value


def redact(text):
    """Sustituye cualquier secreto registrado por `***`."""
    text = str(text)
    for secret in sorted(_SECRETS, key=len, reverse=True):
        text = text.replace(secret, "***")
    return text


def clear():
    """Solo para pruebas."""
    _SECRETS.clear()
