# Imagen única de open-nvr: el subcomando se elige con `command:` en compose
# (web, nvr-watch, egress-watch). busybox aporta /bin/sh para los
# healthchecks CMD-SHELL, la única forma que podman-compose 1.0.6 no altera.
FROM docker.io/library/golang:1.26.10-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/opennvr ./cmd/opennvr

FROM docker.io/library/busybox:1.37.0-musl
COPY --from=build /out/opennvr /opennvr
VOLUME ["/data"]
ENTRYPOINT ["/opennvr"]
