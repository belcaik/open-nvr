# Contexto de construcción: services/ (para incluir common/).
# Fijar también el digest tras el primer pull:
#   podman image inspect --format '{{.Digest}}' docker.io/library/python:3.12.7-slim-bookworm
FROM docker.io/library/python:3.12.7-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    DATA_DIR=/data

WORKDIR /app
COPY common/opennvr_common /app/opennvr_common
COPY egress-watch/egresswatch /app/egresswatch

VOLUME ["/data"]
ENTRYPOINT ["python", "-m", "egresswatch"]
CMD ["run"]
