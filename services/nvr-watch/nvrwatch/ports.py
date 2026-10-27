"""Puertos TCP del escaneo ligero periódico (solo connect, uno cada vez)."""

# Puertos abiertos comprobados en la etapa 1 (docs/test-log.md, T01).
KNOWN_OPEN = (80, 554, 6060, 6066, 8885, 9000)

# Puertos TCP más frecuentes (subconjunto del top 100 de nmap) más puertos
# habituales en NVR/DVR y cámaras: telnet de depuración, protocolos privados
# de otros fabricantes, RTSP alternativos.
TOP = (
    7, 9, 13, 21, 22, 23, 25, 26, 37, 53, 79, 80, 81, 88, 106, 110, 111, 113,
    119, 135, 139, 143, 144, 179, 199, 389, 427, 443, 444, 445, 465, 513, 514,
    515, 543, 544, 548, 554, 587, 631, 646, 873, 990, 993, 995, 1025, 1026,
    1027, 1028, 1029, 1110, 1433, 1720, 1723, 1755, 1900, 2000, 2001, 2049,
    2121, 2717, 3000, 3128, 3306, 3389, 3986, 4899, 5000, 5009, 5051, 5060,
    5101, 5190, 5357, 5432, 5631, 5666, 5800, 5900, 6000, 6001, 6646, 7070,
    8000, 8008, 8009, 8080, 8081, 8443, 8888, 9100, 9999, 10000, 32768, 49152,
    49153, 49154, 49155, 49156, 49157,
)

CAMERA_EXTRA = (
    2323, 4567, 5555, 7547, 8001, 8200, 8554, 8899, 9001, 9527, 9530, 10554,
    32100, 34567, 34599, 37777, 49999,
)


def scan_ports(extra=()):
    return sorted(set(KNOWN_OPEN) | set(TOP) | set(CAMERA_EXTRA) | set(extra))
