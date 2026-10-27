# Baphomet: pasarela y observación del NVR

Baphomet es el host Ubuntu que filtra y registra la salida del NVR. La tabla
`inet opennvr` la instala `opennvr-nft.service`; compose no modifica nftables.
`nvr-watch` observa disponibilidad y configuración, mientras que `ulogd` y
`egress-watch` registran y resumen el tráfico. El perfil `dns` añade un
dnsmasq de lista permitida.

> **Antes de cambiar la puerta de enlace del NVR, Baphomet no puede observar
> su tráfico.** El NVR y el router están en la misma LAN y el tráfico va
> directamente al router; levantar los contenedores o la tabla nft no cambia
> ese recorrido.

## Requisitos previos

- Ubuntu 24.04 en Baphomet con IP LAN estática `192.168.0.8`.
- El NVR en `192.168.0.227`; el router actual, normalmente `192.168.0.1`.
- Acceso administrativo local, `podman`, `podman-compose`, `nftables` y
  `systemd`. El despliegue usa Podman **rootful** porque ulogd necesita abrir
  NFLOG en el espacio de red del host y dnsmasq necesita el puerto 53.
- Nombre de la interfaz LAN conocido (`ip route get 192.168.0.227`).
- Copia exportada de la configuración del NVR y acceso probado a su UI.
- Una ventana de mantenimiento: cambiar la puerta de enlace es un cambio de
  red y puede cortar temporalmente P2P, NTP y acceso remoto.

No se deben publicar puertos del NVR en el router. El acceso remoto al host
se hace por VPN.

## Instalación de Baphomet (todavía sin cambiar el NVR)

1. Instalar el repositorio, por ejemplo en `/opt/open-nvr`, y entrar al
   directorio del despliegue:

   ```bash
   cd /opt/open-nvr/deploy/baphomet
   cp .env.example .env
   chmod 600 .env
   ```

   Revisar `.env`; no escribir contraseñas ni tokens allí si se usan los
   secretos indicados abajo.

2. Comprobar que `sysctl/99-opennvr.conf` usa la interfaz LAN (`enp6s0` en
   Baphomet, verificado el 2026-10-27). Instalar y aplicar sysctl:

   ```bash
   sudo install -m 0644 sysctl/99-opennvr.conf /etc/sysctl.d/99-opennvr.conf
   sudo sysctl --system
   ```

3. Instalar el ruleset y su unidad. La unidad sigue siendo la única encargada
   de aplicar nftables:

   ```bash
   sudo install -d -m 0755 /etc/opennvr
   sudo install -m 0644 nftables/opennvr.nft /etc/opennvr/opennvr.nft
   sudo install -m 0644 systemd/opennvr-nft.service /etc/systemd/system/opennvr-nft.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now opennvr-nft.service
   sudo nft list table inet opennvr
   ```

4. Crear los secretos rootful. `ntfy_token` debe existir aunque ntfy esté
   desactivado; en ese caso se crea vacío.

   ```bash
   read -rsp 'Contraseña NVR: ' NVR_SECRET; echo
   printf '%s' "$NVR_SECRET" | sudo podman secret create nvr_pass -
   unset NVR_SECRET

   read -rsp 'Token ntfy (vacío si no se usa): ' NTFY_SECRET; echo
   printf '%s' "$NTFY_SECRET" | sudo podman secret create ntfy_token -
   unset NTFY_SECRET
   ```

5. Construir y levantar los tres servicios principales:

   ```bash
   sudo podman-compose up -d --build
   sudo podman-compose ps
   ```

   Para activar también el DNS de lista permitida, revisar primero
   `dnsmasq/allow.conf` y ejecutar:

   ```bash
   sudo podman-compose --profile dns up -d --build
   ```

   Con el perfil desactivado, las consultas DNS desviadas por nft fallan. Con
   el perfil activo, cualquier nombre no incluido en `allow.conf` devuelve
   NXDOMAIN. Resolver un nombre no lo autoriza por sí solo: su IP, protocolo y
   puerto también deben figurar en `nvr_allow4`.

## Paso separado: cambiar la puerta de enlace del NVR

Este paso es el que coloca a Baphomet en el camino. Confirmar antes que la
tabla nft y los contenedores están sanos.

Mantener la IP estática del NVR en **`192.168.0.227`** y cambiar solamente su
puerta de enlace a **`192.168.0.8`**. Hay dos vías:

- En la UI web del NVR, abrir la configuración de red, conservar IP, máscara
  y DNS, establecer Gateway en `192.168.0.8`, guardar y volver a consultar la
  pantalla.
- Por la API propietaria: guardar primero `GET /Network/Interfaces`, editar
  una copia del XML cambiando solo el Gateway y enviar esa copia mediante
  `PUT /Network/Interfaces`. No reutilizar un XML de ejemplo: deben
  conservarse los campos y la versión que devuelve el equipo.

  ```bash
  curl --fail --silent --show-error --user "$NVR_USER:$NVR_PASS" \
    "http://192.168.0.227/Network/Interfaces" > /tmp/nvr-interfaces.before.xml
  cp /tmp/nvr-interfaces.before.xml /tmp/nvr-interfaces.put.xml
  # Editar /tmp/nvr-interfaces.put.xml: Gateway 192.168.0.8, nada más.
  curl --fail --silent --show-error --user "$NVR_USER:$NVR_PASS" \
    -X PUT -H 'Content-Type: application/xml' \
    --data-binary @/tmp/nvr-interfaces.put.xml \
    "http://192.168.0.227/Network/Interfaces"
  ```

La forma exacta del XML se toma del `GET` y, si la API rechaza el `PUT`, se
usa la UI; no se prueban esquemas a ciegas. Mantener una sesión LAN directa
para poder volver inmediatamente a `192.168.0.1`.

## Verificación del recorrido y del aislamiento

No basta con que los contenedores estén `healthy`. Las siguientes pruebas
demuestran que el NVR realmente usa Baphomet:

1. **Gateway declarado.** `GET /Network/Interfaces` debe mostrar IP
   `192.168.0.227` y Gateway `192.168.0.8`.

2. **Contadores de origen.** Tomar dos lecturas separadas mientras se esperan
   reintentos P2P/DNS del NVR:

   ```bash
   sudo nft list counters table inet opennvr
   sleep 60
   sudo nft list counters table inet opennvr
   ```

   Los paquetes de `nvr_from_seen` —la regla `ip saddr 192.168.0.227`— deben
   aumentar. `nvr_out_dropped`, `nvr_out_allowed` o `nvr_dns_redirected`
   detallan el resultado. Si `nvr_from_seen` no aumenta, Baphomet aún no ve la
   salida del NVR y la prueba falla.

3. **P2P deja de estar listo para Internet.** Consultar periódicamente
   `GET /Network/P2PV2`. Con lista permitida vacía, el estado debe abandonar
   `internet-ready` tras agotar sus sesiones/reintentos. Si sigue
   `internet-ready` mientras `nvr_out_dropped` aumenta, esperar a que caduque
   la sesión; si persiste, buscar una vía de bypass en el router o por IPv6.

4. **No se envían redirects ICMP.** Comprobar los tres sysctl efectivos y el
   contador defensivo:

   ```bash
   sysctl net.ipv4.conf.all.send_redirects net.ipv4.conf.default.send_redirects \
     net.ipv4.conf.enp6s0.send_redirects
   sudo nft list counter inet opennvr redirects_to_nvr
   ```

   Los sysctl deben valer `0`; `redirects_to_nvr` debe permanecer en cero. Una
   captura de 60 s durante reintentos completa la prueba: no debe aparecer
   ningún ICMP tipo 5 dirigido a `192.168.0.227`.

5. **Sin bypass IPv6.** Capturar en la interfaz LAN del router o en un puerto
   espejo del switch (no solo en Baphomet) tráfico con origen MAC
   `5a:5a:02:5b:e4:18` e IPv6. No debe haber conexiones hacia destinos IPv6
   globales. Si aparecen, bloquear esa MAC para IPv6 en el router/switch o
   desactivar IPv6 en el NVR; la tabla de Baphomet solo puede descartar IPv6
   que efectivamente se enrute por Baphomet. Si el NVR tiene una dirección
   global conocida, añadirla a `nvr_addr6` y verificar que
   `nvr_v6_dropped` aumenta.

6. **Sin bypass directo al router.** En la interfaz LAN del router, capturar o
   consultar sus logs/sesiones. No debe llegar tráfico encaminado con origen
   `192.168.0.227`: Baphomet hace masquerade y el router debe verlo con origen
   `192.168.0.8`. Confirmar además que el Gateway del NVR sigue siendo `.8`.
   Cualquier flujo `.227` observado directamente por el router invalida la
   prueba aunque nft muestre otros paquetes.

7. Revisar el pipeline de registro:

   ```bash
   sudo podman-compose ps
   sudo podman logs --since 10m opennvr-ulogd
   sudo podman logs --since 10m opennvr-egress-watch
   sudo podman exec opennvr-egress-watch python -m egresswatch summary
   ```

Más detalles sobre alertas y límites están en `docs/monitoring.md`.

## Vuelta atrás

El orden evita dejar al NVR enrutado por un host sin filtro:

1. En la UI o con el XML guardado, devolver primero la puerta de enlace del
   NVR a `192.168.0.1`. Confirmar con `GET /Network/Interfaces` y comprobar
   que el acceso normal se recuperó.
2. Solo entonces detener los contenedores:

   ```bash
   sudo podman-compose --profile dns down
   ```

3. Desactivar la unidad y borrar únicamente la tabla propia:

   ```bash
   sudo systemctl disable --now opennvr-nft.service
   sudo nft delete table inet opennvr
   ```

4. Retirar `/etc/sysctl.d/99-opennvr.conf` y ejecutar `sudo sysctl --system`,
   o restaurar manualmente los valores previos documentados antes de instalar.

Los volúmenes con líneas base, estado y logs no se borran con `down`; se
conservan para diagnóstico. Eliminarlos requiere una decisión separada.
