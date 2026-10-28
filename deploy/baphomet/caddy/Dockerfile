# Caddy con el módulo DNS de Cloudflare, para certificados de Let's Encrypt
# por DNS-01 sin exponer el puerto 80 a Internet.
FROM docker.io/library/caddy:2.11.7-builder AS build
RUN xcaddy build --with github.com/caddy-dns/cloudflare@v0.2.4

FROM docker.io/library/caddy:2.11.7
COPY --from=build /usr/bin/caddy /usr/bin/caddy
