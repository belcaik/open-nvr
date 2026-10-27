"""python -m nvrwatch [run|once|accept|show]"""
import sys
import time

from opennvr_common.log import log

from . import app, config


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    cmd = argv[0] if argv else "run"
    cfg = config.from_env()
    if not cfg.password:
        log("aviso: sin NVR_PASS/NVR_PASS_FILE; la API y el RTSP fallarán con 401")
    if cmd == "run":
        app.App(cfg).run_forever()
        return 0
    if cmd == "once":
        a = app.App(cfg)
        a.state["last_scan"] = time.time()  # `once` no escanea puertos
        a.state["last_baseline"] = 0
        a.tick(time.time())
        return 0
    if cmd == "accept":
        return app.accept(cfg)
    if cmd == "show":
        return app.show(cfg)
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main())
