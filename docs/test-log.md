# Registro de pruebas

Cada entrada indica fecha, prueba, comando (sin credenciales), resultado y
dónde está la evidencia. La evidencia bruta está en `captures/`, que no se
versiona porque contiene números de serie, identificadores P2P y MAC.

Credenciales: `~/.config/open-nvr/env` (modo 600, fuera del repositorio) con
`NVR_HOST`, `NVR_USER` y `NVR_PASS`. Los comandos se ejecutan con
`set -a; . ~/.config/open-nvr/env; set +a`.

Host de pruebas: Arch Linux en `192.168.0.159` (sin root, así que no hay
escaneo UDP con nmap ni captura de paquetes).

## Etapa 1: descubrimiento activo (2026-10-26)

| ID | Prueba | Resultado | Evidencia |
|---|---|---|---|
| T00 | Barrido de la LAN (`nmap -sn 192.168.0.0/24`) | 6 hosts activos. La única MAC `5a:5a:02:…` visible es la del NVR | `captures/T00-lan-sweep.txt` |
| T01 | TCP completo (`nmap -sT -p- --max-rate 200`) | Abiertos: **80, 554, 6060, 6066, 8885, 9000**. Los otros 65 529 cerrados (RST) | `captures/T01-tcp-all.*` |
| T01b | UDP selectivo (datagrama vacío, socket conectado) | `open\|filtered`: 3702, 5555, 32108. Cerrados con ICMP: 53, 123, 1900, 6060, 6066, 8000, 10000, 32100, 34567, 37777 | `captures/T01-udp-selected.txt` |
| T02 | Banners | HTTP `Boa/0.94.14rc21` (estáticos) y `CGI/1.6.3` (API). RTSP `RtpRtspFlyer`. 6060, 6066, 8885 y 9000 no envían banner y nmap `-sV` no los identifica | `captures/T02-*.txt` |
| T02b | Autenticación HTTP | API protegida con **HTTP Basic** (`realm="IP-Camera"`). Sin TLS | `captures/api/` |
| T02c | Autenticación RTSP | El servidor ofrece **Digest** (`realm="turing"`) **y Basic** | `captures/T02-rtsp-describe-noauth.txt` |
| T03 | Canales `/live/{0..3}/{MAIN,SUB}` | `/live/0` y `/live/1` responden y muestran **el mismo encuadre** (canal 1). `/live/2` y `/live/3` no devuelven flujo | `captures/T03-rtsp-channels.txt`, `frame-live{0,1}.jpg` |
| T04 | Parámetros de flujo | MAIN: HEVC 2304×1296 a 10 fps. SUB: HEVC 800×448 a 10 fps. Audio PCMU 8 kHz mono en ambos | `captures/T03-rtsp-channels.txt` |
| T05 | Sesiones simultáneas | No ejecutada | — |
| T06 | Tráfico saliente del NVR | No ejecutable desde este host (sin root y sin estar en el camino del tráfico). Pasa a la etapa 3 | — |
| T10 | WS-Discovery (multicast y unicast, con y sin `Types`) | **Sin respuesta**. El 3702/udp no devuelve ICMP, pero tampoco contesta | `captures/T10-wsdiscovery.txt` |
| T11 | `GetSystemDateAndTime` en `/onvif/device_service` (puertos 80, 8885, 9000) | Puerto 80: la conexión se acepta y se agota sin respuesta. 8885 y 9000: cierran sin respuesta HTTP | — |
| T12 | Identidad | ONVIF no disponible. Se obtiene por la API propietaria `GET /System/DeviceInfo` (ver hardware.md) | `captures/api/System_DeviceInfo.xml` |
| T13 | Perfiles por canal | ONVIF no disponible. Topología por `GET /System/FrontConnectDevicetList`: **1 cámara registrada** | `captures/api/System_FrontConnectDevicetList.xml` |
| — | Interfaz web | Copia estática de la UI (24 páginas y sus scripts) para analizar la API sin enviar peticiones a ciegas | `captures/web/` |

### Conclusiones de la etapa 1

1. **El NVR no ofrece ONVIF** en ningún puerto ni por WS-Discovery. Su
   control es una API HTTP/XML propietaria (Basic auth) más un protocolo
   binario en 6060/6066 que usa el cliente EasyVideo.
2. **Solo hay una cámara conectada** (canal 1). Los canales 2–10 están vacíos.
3. **La cámara está en un segmento PoE aislado**. Tiene una IP del rango de la
   LAN (`192.168.0.14`), pero no responde a ping ni a ARP desde la LAN. El NVR
   la gestiona por el protocolo privado (puerto 6060, `ProtocolType=3`), no
   por ONVIF ni RTSP.
4. `/live/0/*` y `/live/1/*` son alias del canal 1. Con varias cámaras
   habrá que confirmar si `/live/N` es base 1 (hipótesis: `/live/0` es un alias
   de compatibilidad).
5. El reloj del NVR usa la zona `CST-08:00` (China). La hora mostrada va 8 h
   por delante de UTC y no coincide con la zona local.
6. Los endpoints `/System/DeviceCapV2` y `/System/AICap` no responden (la
   conexión se agota). Otros, como `/System/LightCap/1` o
   `/System/CloudUpgradeServerInfo`, devuelven códigos de error de la API
   (`statusCode` -1 o -3).

## Etapa 2: control de la cámara (2026-10-26)

PTZ por `PUT /PTZ/1/{cmd}` con cuerpo `Param1=1` (arranca) o `Param1=0`
(para) y `Param2=<velocidad 1–10>`. Cada movimiento se comprobó con fotogramas
antes y después (`captures/ptz/`).

| ID | Prueba | Resultado | Evidencia |
|---|---|---|---|
| T20 | Pan: `TurnLeft` 1 s, `TurnRight` 1 s | ✅ `statusCode 0`; el encuadre cambia y vuelve | `ptz/t20-montage.jpg` |
| T21 | Zoom: `ZoomIn` 2 s, `ZoomOut` 3 s. Tilt: `TurnUp`/`TurnDown` 1 s | ✅ El OSD pasa de 1.0X a **6.8X** y vuelve. El tilt funciona | `ptz/t21-montage.jpg` |
| T22 | Presets: `Presets/Set`, mover, `Presets/Goto`, `Presets/Remove` | Índice 200: responde 0 pero **no mueve** (fuera de rango, hipótesis). Índice 2: ✅ vuelve a la posición. Los presets de prueba se borraron | `ptz/t22-montage.jpg`, `t22b-montage.jpg` |
| T23 | Luz: `PUT /PTZ/1/Light` con `Param1=1` y luego `0` | ✅ Responde. `0` apaga la iluminación (imagen negra) de forma temporal; el modo "inteligente" la restablece sola y `IrCutFilter` no cambia | `ptz/t23-montage.jpg`, `api/IrCutFilter-after-light.xml` |
| T17 | Backchannel RTSP: `DESCRIBE` con `Require: www.onvif.org/ver20/backchannel` | ❌ Lo ignora: el SDP es idéntico, sin pista `sendonly`. El SDP incluye `MEDIAINFO=494D4B48…` ("IMKH") | `T17-describe*.txt` |
| T30 | Eventos por WebSocket (`fun=event`, auth `md5(pass+epoch_ms)`) | ✅ Autentica (`code 0`). No llegan eventos porque la detección de movimiento está desactivada | `T30-ws-events.txt` |
| T31 | Captura JPEG por HTTP | ✅ 800×448 | `snapshot.jpg` |
| T32 | Grabaciones | `RecordMode=stop`, disco sin formatear. `GetRecordInfoByMonth` marca un día con registro, pero no hay nada que reproducir. La reproducción usa el protocolo privado | `api/Disk.xml`, `api/Record.xml` |
| T33 | Foco e iris (`FocusFar/Near`, `ApertureBigger/Smaller`) | No probado | — |

### Conclusiones de la etapa 2

- Pan, tilt, zoom, presets y luz se controlan desde Linux con HTTP estándar
  (curl). No hace falta el cliente de Windows.
- El **audio bidireccional** no está disponible por RTSP ni ONVIF. La UI lo
  envía al cliente EasyVideo local (WebSocket en `127.0.0.1`), que habla con el
  NVR por el protocolo privado P6S (6060/6066). Para implementarlo hay que
  capturar el tráfico del cliente oficial y documentar el protocolo. No se
  envía al equipo ninguna estructura binaria sin documentar.
- Las grabaciones requieren primero formatear el disco y activar la
  grabación. Formatear necesita tu confirmación expresa.

## Etapa 2b: grabación y herramientas (2026-10-27)

| ID | Prueba | Resultado | Evidencia |
|---|---|---|---|
| T40 | Formatear el disco con `PUT /Record/Format/Call` (cuerpo vacío, como la UI). **Autorizado expresamente** por el propietario | ✅ `statusCode 0`. El disco pasa por `diskexception`, luego `idle` y luego `writting`. Capacidad libre: 473 240 MB | `captures/T40-format.txt`, `api/Disk-before-format.xml` |
| T41 | ¿Graba? `GetRecordInfoByDay` del canal 1 | ✅ Tramo `general` desde las 11:18:33 (hora del NVR) que sigue creciendo. La programación ya era 24/7 (`TimeBlock_*` todo a `1`). `RecordMode=stop` y `DiskFormatStatus=unformatted` no cambian: no reflejan el estado real | `captures/T40-recordinfo-day.xml` |
| T42 | Captura de la VM sin root (`filter-dump` de QEMU) | ✅ Con la NIC e1000e. ❌ Con virtio y vhost-net (`Vhost is not supported`): en ese caso, `tcpdump` en `vnetN` con sudo | `tools/vm_capture.sh` |
| T43 | `tools/nvr_gateway.py plan` | ✅ Solo cambia la puerta de enlace, el DNS y DHCP. No se ha aplicado | — |
