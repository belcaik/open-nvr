# Baphomet: pasarela y observación del NVR

Baphomet es el host Ubuntu que filtra y registra la salida del NVR. La tabla
`inet opennvr` la instala `opennvr-nft.service`; compose no modifica nftables.
`nvr-watch` observa disponibilidad y configuración, mientras que `ulogd` y
`egress-watch` registran y resumen el tráfico. dnsmasq responde el DNS del
NVR con una lista permitida y ntfy envía los avisos.

> **podman-compose 1.0.6** (Ubuntu 24.04) no admite `--profile` e ignora
> `profiles:`, así que este compose no los usa: `up -d` levanta siempre los
> cinco servicios.

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

1. Descargar **solo** `deploy/baphomet/` del repositorio público (sparse
   checkout, sin el resto del código ni la documentación). Las imágenes se
   construyen en GitHub Actions y se descargan de `ghcr.io/belcaik/open-nvr-*`,
   así que en el servidor no se compila nada:

   ```bash
   sudo git clone --depth 1 --filter=blob:none --sparse \
     https://github.com/belcaik/open-nvr.git /opt/open-nvr
   sudo git -C /opt/open-nvr sparse-checkout set deploy/baphomet
   cd /opt/open-nvr/deploy/baphomet
   sudo cp .env.example .env
   sudo chmod 600 .env
   ```

   Para actualizar más adelante: `sudo git -C /opt/open-nvr pull` y
   `sudo podman-compose pull`. Para fijar una versión concreta, poner
   `OPENNVR_TAG=sha-<commit>` en `.env`.

   Revisar `.env`; no escribir contraseñas ni tokens allí si se usan los
   secretos indicados abajo.

2. Comprobar que `sysctl/99-opennvr.conf` usa la interfaz LAN (`enp6s0` en
   Baphomet, verificado el 2026-10-27). Instalar y aplicar sysctl:

   ```bash
   sudo install -m 0644 sysctl/99-opennvr.conf /etc/sysctl.d/99-opennvr.conf
   sudo sysctl --system
   ```

   Comprobar que se aplicó. Los tres `send_redirects` **deben** valer 0; el
   archivo instalado no basta si no se aplicó:

   ```bash
   sysctl net.ipv4.ip_forward net.ipv4.conf.all.send_redirects \
     net.ipv4.conf.default.send_redirects net.ipv4.conf.enp6s0.send_redirects
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

5. Descargar las imágenes y levantar los tres servicios principales:

   Los secretos `nvr_pass` y `ntfy_token` del paso 4 **deben existir antes**:
   si falta alguno, `podman run` de `nvr-watch` o `egress-watch` falla y
   podman-compose sigue con los demás sin avisar
   (`sudo podman secret ls`).

   ```bash
   sudo podman-compose pull
   sudo podman-compose up -d
   sudo podman ps -a --filter label=io.podman.compose.project=baphomet
   ```

   dnsmasq responde a cualquier nombre que no esté en `dnsmasq/allow.conf`
   con NXDOMAIN. Resolver un nombre no lo autoriza por sí solo: su IP, protocolo y
   puerto también deben figurar en `nvr_allow4`.

6. Arranque tras un reinicio o corte de luz. Con Podman, `restart:
   unless-stopped` solo se aplica al arrancar el host si
   `podman-restart.service` está habilitado (en rootful, a nivel de sistema):

   ```bash
   sudo systemctl enable --now podman-restart.service
   ```

## ntfy local

Si no hay otro servidor ntfy, este compose levanta uno en
`http://192.168.0.8:2586`, sin acceso anónimo (`deny-all`) y publicado solo en
la IP de la LAN. Sustituye al contenedor rootless `ntfy` de `~/pod/ntfy`,
que nunca llegó a arrancar (estado `Created`) y tenía la URL de ejemplo
`ntfy.tudominio.com`. Para no dejar dos definiciones, se puede borrar:
`podman rm ntfy` (sin `sudo`; no tenía datos).

1. En `.env`: `NTFY_URL=http://ntfy:2586`, `NTFY_BASE_URL=http://192.168.0.8:2586`,
   `NTFY_BIND=192.168.0.8` y `NTFY_PORT=2586`. En Baphomet el 8080 lo ocupa
   otro contenedor (`gta-v-map`). Antes de elegir un puerto, comprobar que
   está libre con `ss -ltn 'sport = :2586'`.

2. Levantar ntfy y crear un usuario administrador (para la app móvil) y un
   usuario de servicio con permiso solo de escritura en el tema:

   ```bash
   sudo podman-compose up -d ntfy
   sudo podman exec -it opennvr-ntfy ntfy user add --role=admin admin
   sudo podman exec -it opennvr-ntfy ntfy user add opennvr
   sudo podman exec opennvr-ntfy ntfy access opennvr opennvr write-only
   sudo podman exec opennvr-ntfy ntfy token add opennvr   # copiar el tk_…
   ```

3. Guardar el token como secreto (sustituye al vacío del paso 4) y recrear
   los servicios que lo leen:

   ```bash
   sudo podman secret rm ntfy_token
   read -rsp 'Token ntfy: ' NTFY_SECRET; echo
   printf '%s' "$NTFY_SECRET" | sudo podman secret create ntfy_token -
   unset NTFY_SECRET
   sudo podman-compose up -d --force-recreate nvr-watch egress-watch
   ```

4. En la app ntfy del móvil: servidor `http://192.168.0.8:2586` (solo desde
   la LAN o por VPN), usuario `admin` y suscripción al tema `opennvr`.

5. Prueba de extremo a extremo:

   ```bash
   sudo podman exec opennvr-ntfy ntfy publish \
     --token "$(sudo podman secret inspect --showsecret ntfy_token --format '{{.SecretData}}')" \
     http://127.0.0.1:2586/opennvr "prueba open-nvr"
   ```

   Si `--showsecret` no está disponible en Podman 4.9, publicar desde la app
   con el usuario `admin`.

Para levantar todo de una vez en adelante:
`sudo podman-compose up -d`.

## Hora del NVR (chrony)

Con la salida bloqueada, el NVR no llega a ningún servidor NTP público. El
contenedor `opennvr-chrony` sirve en `192.168.0.8:123/udp` la hora de
Baphomet, que ya sincroniza `systemd-timesyncd`. chronyd corre con `-x` (no
toca el reloj del host), sin fuentes propias (`local stratum 10`) y solo
responde a `192.168.0.227`. El tráfico del NVR hacia Baphomet no se reenvía:
lo cuenta `nvr_to_host` en nft.

1. Comprobar que el reloj de Baphomet está sincronizado:
   `timedatectl show -p NTPSynchronized` debe devolver `yes`.
2. Levantarlo con el resto: `sudo podman-compose up -d chrony`.
3. Apuntar el NTP del NVR a Baphomet (`PUT /System/NTP` con
   `ServerName=192.168.0.8`, el mismo XML que devuelve el `GET`, o desde
   Configuración > Sistema > Fecha y hora en la UI).

## Paso separado: cambiar la puerta de enlace del NVR

Este paso es el que coloca a Baphomet en el camino. Confirmar antes que la
tabla nft y los contenedores están sanos.

El NVR usa **DHCP** (verificado el 2026-10-27). Si solo se cambiara la puerta
de enlace, el router la devolvería a `192.168.0.1` al renovar la concesión.
Por eso el cambio fija la IP actual (`192.168.0.227`), desactiva DHCP y pone
la puerta de enlace en **`192.168.0.8`**. También
el DNS en `192.168.0.8`. Conviene reservar además `192.168.0.227` en el DHCP
del router.

`tools/nvr_gateway.py` lo hace igual que la pantalla de red de la UI: lee el
XML de `GET /Network/Interfaces`, cambia esos nodos, lo envía con `PUT` y
guarda una copia en `captures/network/` para volver atrás. Se ejecuta desde
un equipo de la LAN que tenga el repositorio y las credenciales:

```bash
set -a; . ~/.config/open-nvr/env; set +a
tools/nvr_gateway.py plan  --gateway 192.168.0.8 --dns 192.168.0.8   # solo muestra el cambio
tools/nvr_gateway.py apply --gateway 192.168.0.8 --dns 192.168.0.8
tools/nvr_gateway.py rollback                                       # vuelta atrás
```

Mantener una sesión LAN directa con el NVR (misma subred, sin pasar por
Baphomet) para poder ejecutar `rollback` en cualquier momento.

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
   sudo podman-compose down
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
