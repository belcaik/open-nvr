"""Healthcheck del contenedor: /healthz debe responder 200.

Va como módulo porque podman-compose 1.0.6 pasa el test por /bin/sh y un
`python -c` con paréntesis rompe la sintaxis.
"""

import os
import sys
import urllib.request


def main() -> int:
    listen = os.getenv("DETECT_LISTEN", "127.0.0.1:8556")
    try:
        with urllib.request.urlopen(f"http://{listen}/healthz", timeout=3) as response:
            return 0 if response.status == 200 else 1
    except OSError:
        return 1


if __name__ == "__main__":
    sys.exit(main())
