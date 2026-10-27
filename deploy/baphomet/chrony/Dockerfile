# Fijar también el digest tras el primer pull:
#   podman image inspect --format '{{.Digest}}' docker.io/library/alpine:3.20.3
FROM docker.io/library/alpine:3.20.3

RUN apk add --no-cache chrony

COPY chrony.conf /etc/chrony/chrony.conf

# -d: primer plano y log a stderr. -x: no ajusta el reloj del host (lo
# sincroniza systemd-timesyncd); solo sirve esa hora. -u root: sin cambio de
# usuario, como dnsmasq.
ENTRYPOINT ["chronyd", "-d", "-x", "-u", "root", "-f", "/etc/chrony/chrony.conf"]
