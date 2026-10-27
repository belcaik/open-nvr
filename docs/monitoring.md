# Monitorización en Baphomet

## Diseño

La monitorización tiene dos planos independientes. `nvr-watch` realiza
sondas de lectura contra el NVR y compara respuestas normalizadas con una
línea base. El plano de tráfico se apoya en nftables: las reglas cuentan cada
paquete, envían metadatos a NFLOG y deciden si se permite o descarta. `ulogd`
lee NFLOG y escribe JSON; `egress-watch` sigue esos archivos, los agrega y
envía avisos. El perfil opcional `dnsmasq` registra los nombres consultados.

```text
NVR --HTTP/RTSP--> nvr-watch --> línea base/estado --> ntfy
 |
 +--tráfico enrutado--> nftables --grupo NFLOG 1/2--> ulogd --> JSON --+
 +--DNS redirigido----------------------------------> dnsmasq --> log --+--> egress-watch --> ntfy
```

La fuente de verdad sobre si Baphomet ve tráfico son los contadores de la
tabla `inet opennvr`, no el estado `healthy` de los contenedores. Antes de
cambiar el Gateway del NVR a `192.168.0.8`, ese tráfico no pasa por Baphomet.

## Flujos de datos

### nvr-watch

Cada `CHECK_INTERVAL` comprueba por TCP los puertos conocidos, hace un GET de
`/System/DeviceInfo` y un RTSP `DESCRIBE`. Tras `FAIL_THRESHOLD` fallos
consecutivos declara caída; el primer éxito declara recuperación.

Cada `BASELINE_INTERVAL` lee DeviceInfo, cámaras registradas y su estado,
P2P, NTP, interfaces y DeviceStatus. El XML se aplana a claves estables. Se
eliminan usuarios y contraseñas y se guardan hashes cortos de UUID, DID y
números de serie. Solo se comparan las secciones obtenidas en esa ronda, para
que un endpoint fallido no parezca una eliminación masiva.

El escaneo TCP periódico es secuencial, de bajo ritmo y solo contra el NVR.
Su última lista válida de puertos se incorpora a la instantánea. La línea base
vive en el volumen de `/data` y no cambia ante diferencias hasta ejecutar
explícitamente `python -m nvrwatch accept`; una sección nunca vista sí se
incorpora automáticamente.

### nftables, ulogd y egress-watch

El grupo NFLOG 1 contiene paquetes reenviados permitidos; el grupo 2 contiene
descartes, incluidos salida, entrada, IPv6 y MAC del NVR usando otra IPv4.
`ulogd` escribe una línea JSON por paquete en `nvr-fwd.json` o
`nvr-drop.json`. Los archivos viven en un volumen compartido de solo lectura
para `egress-watch`.

`egress-watch` conserva inodo y desplazamiento, tolera rotación/truncado y
solo consume líneas completas. Agrega por acción, dirección, IP remota,
puerto y protocolo. Las respuestas permitidas se atribuyen al flujo saliente
que las originó. Los archivos completamente consumidos se vacían cuando
superan `LOG_MAX_BYTES`.

dnsmasq escribe cada consulta y respuesta en otro volumen. `egress-watch`
procesa DNS primero para poder anotar una IP con el último nombre que la
resolvió. Las consultas de otros clientes se ignoran cuando contienen su IP;
respuestas sin cliente sirven únicamente para el mapa nombre-IP.

El estado, los offsets, la media móvil, los destinos conocidos y el límite de
avisos sobreviven reinicios en el volumen `/data` de cada servicio.

## Significado de los avisos

### Avisos de nvr-watch

- **`NVR: inaccesible (192.168.0.227)`**: todos los puertos TCP conocidos
  están declarados caídos. Puede ser pérdida de energía/red o una IP cambiada.
- **`NVR: fallo: …`**: una o más sondas concretas cruzaron el umbral: puerto,
  API HTTP, RTSP, hora, Gateway, P2P o cámara.
- **`NVR: recuperado: …`**: la sonda volvió a responder; incluye cuánto duró
  la caída cuando se conoce.
- **`hora del NVR`**: la diferencia de la cabecera HTTP `Date` supera
  `TIME_DRIFT_MAX`.
- **`puerta de enlace del NVR`**: el XML de interfaces no coincide con
  `EXPECTED_GATEWAY`. Es una señal directa de posible bypass.
- **`P2P aislado de Internet`**: con `EXPECT_P2P_OFFLINE=true`, algún valor de
  la sección P2P coincide con `P2P_ONLINE_STATES` (por defecto
  `internet-ready`); el aislamiento no está demostrado.
- **`cámara del canal N`**: una cámara registrada dejó de informar
  `connect success`.
- **`línea base creada`**: primera instantánea válida; es informativo.
- **`N cambios respecto a la línea base`**: firmware, identidad hasheada,
  red, P2P, NTP, cámaras o puertos cambiaron. Revisar con
  `python -m nvrwatch show` y aceptar solo un cambio esperado con
  `python -m nvrwatch accept`.

Los campos volátiles configurados en `IGNORE_PREFIXES` no provocan cambios de
línea base. Los fallos de un endpoint se ven por disponibilidad, no como
claves eliminadas.

### Avisos de egress-watch

- **`N destinos nuevos del NVR`**: aparecieron claves nunca vistas. Distingue
  `PERMITIDO`, `bloqueado`, entrada bloqueada, IPv6 bloqueado, spoof de MAC y
  consultas DNS. Un destino nuevo permitido tiene prioridad mayor porque la
  política lo dejó salir.
- **`pico de tráfico permitido`**: una ventana supera `SPIKE_MIN_BYTES` y
  `SPIKE_FACTOR` veces la EWMA anterior. Muestra los cinco flujos con más
  bytes. El tráfico descartado nunca genera picos.
- **`sin tráfico del NVR registrado`**: durante `SILENCE_ALERT_HOURS` no llegó
  ningún paquete a ulogd. Puede significar NVR apagado, Gateway cambiado,
  ruleset ausente o pipeline NFLOG roto; no significa por sí solo que el NVR
  esté tranquilo.
- **`vuelve a haber tráfico del NVR registrado`**: final del silencio.
- **`resumen diario`**: paquetes/bytes permitidos, paquetes descartados,
  cantidad de novedades, principales flujos y DNS. Sale una vez al día
  después de `SUMMARY_HOUR`, usando la hora local del contenedor.

Todos los avisos pasan por deduplicación (`NOTIFY_MIN_INTERVAL`) y por un
máximo global (`NOTIFY_MAX_PER_HOUR`). El siguiente aviso entregado indica
cuántos se suprimieron. Si el POST a ntfy falla, se revierte la deduplicación
para permitir reintento. Con `NTFY_URL` vacío, el aviso solo se registra.

## Límites y supuestos

- nftables solo ve paquetes que atraviesan Baphomet. Un Gateway revertido,
  una ruta directa al router, IPv6 directo o otra interfaz son puntos ciegos.
  Hay que verificar contadores y el router como describe el README de
  Baphomet.
- NFLOG registra paquetes, no sesiones completas ni contenido. Puede perder
  eventos si se llena el búfer; ulogd o el disco caído no detienen el filtrado
  de nftables, pero sí dejan huecos de observación.
- El esquema JSON exacto depende de la versión del plugin ulogd. El parser
  admite las dos familias de nombres conocidas, pero debe validarse con una
  línea real tras instalar. Campos desconocidos o JSON inválido se ignoran.
- Los bytes representan longitudes observadas por paquete. En IPv6 puede ser
  payload en vez de longitud total según el campo emitido; no es contabilidad
  de facturación.
- La correlación DNS es aproximada: varias respuestas, CDN, TTL, cachés y
  nombres que comparten IP pueden hacer que el último nombre no sea el que
  originó un flujo. DNS cifrado o direcciones literales no aportan nombre.
- `dnsmasq` está desactivado salvo perfil `dns`. Sin él, el DNAT de DNS hace
  fallar las consultas; con él, `allow.conf` autoriza resolución, no tráfico.
- El estado P2P procede de una API propietaria y sus nombres no están
  documentados. La comprobación busca valores configurables, no garantiza que
  toda conectividad cloud haya terminado.
- El reloj se estima con HTTP `Date`; una cabecera incorrecta, cacheada o sin
  zona produce una alerta o deja la comprobación sin dato.
- Un escaneo sin puertos abiertos no reemplaza la última lista válida. Así se
  evita una falsa eliminación masiva durante una caída, a costa de conservar
  datos antiguos hasta el siguiente escaneo exitoso.
- El límite de destinos y claves diarias protege memoria. Al alcanzarlo se
  expulsan claves conocidas antiguas o se dejan de agregar claves diarias
  nuevas; los contadores nft siguen siendo la referencia de bajo nivel.
