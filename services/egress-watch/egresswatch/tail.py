"""Lectura incremental de un archivo de registro que otro proceso va ampliando.

Guarda inodo y desplazamiento en un dict que el llamador persiste. Detecta
rotación (cambia el inodo) y truncado (el tamaño baja del desplazamiento) y
en ambos casos vuelve al principio. Solo consume líneas completas.

Sin cron no hay logrotate: `truncate_if_larger` vacía el archivo cuando ya
se ha leído entero y supera un tamaño. Es seguro con escritores que abren en
modo append (ulogd2 y dnsmasq): su siguiente escritura va al nuevo final.
"""
import os

MAX_READ = 4 << 20


class FileTail:
    def __init__(self, path, state):
        self.path = path
        self.state = state
        state.setdefault("inode", None)
        state.setdefault("offset", 0)

    def read_lines(self, max_bytes=MAX_READ):
        try:
            st = os.stat(self.path)
        except FileNotFoundError:
            return []
        if st.st_ino != self.state["inode"] or st.st_size < self.state["offset"]:
            self.state["inode"] = st.st_ino
            self.state["offset"] = 0
        if st.st_size == self.state["offset"]:
            return []
        with open(self.path, "rb") as fh:
            fh.seek(self.state["offset"])
            data = fh.read(max_bytes)
        end = data.rfind(b"\n")
        if end < 0:
            return []
        self.state["offset"] += end + 1
        return data[:end].decode("utf-8", errors="replace").splitlines()

    def truncate_if_larger(self, max_size):
        """Vacía el archivo si se ha leído completo y pesa más de `max_size`."""
        if max_size <= 0:
            return False
        try:
            st = os.stat(self.path)
        except FileNotFoundError:
            return False
        if st.st_ino == self.state["inode"] and st.st_size == self.state["offset"] \
                and st.st_size > max_size:
            os.truncate(self.path, 0)
            self.state["offset"] = 0
            return True
        return False
