# Fijar también el digest tras el primer pull:
#   podman image inspect --format '{{.Digest}}' docker.io/library/alpine:3.20.3
FROM docker.io/library/alpine:3.20.3

RUN apk add --no-cache 'dnsmasq~2.90' \
 && mkdir -p /var/log/dnsmasq /etc/dnsmasq.d

ENTRYPOINT ["dnsmasq", "--keep-in-foreground", "--conf-file=/etc/dnsmasq.d/opennvr.conf"]
