# Fijar también el digest tras el primer pull:
#   podman image inspect --format '{{.Digest}}' docker.io/library/debian:bookworm-20241016-slim
FROM docker.io/library/debian:bookworm-20241016-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ulogd2 ulogd2-json \
 && rm -rf /var/lib/apt/lists/* \
 && ln -s "$(dirname "$(dpkg -L ulogd2 | grep '/ulogd_inppkt_NFLOG.so$')")" /opt/ulogd \
 && mkdir -p /var/log/ulogd

COPY ulogd.conf /etc/ulogd.conf

# Sin -d: se queda en primer plano para podman.
ENTRYPOINT ["ulogd", "-c", "/etc/ulogd.conf"]
