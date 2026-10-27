# Hardware: kit 2NLF GAD3035WT2

Inventario verificado el 2026-10-26/27. Los números de serie, ChipID, UID y DID
de P2P y la SubKey se omiten a propósito; están en `captures/api/`, que no se
versiona. Leyenda al final.

## Inventario

| Elemento | Detalle | Estado |
|---|---|---|
| Kit | 2NLF GAD3035WT2 | ✅ Etiqueta |
| NVR, modelo interno | `N310B-11NEA-AI-Z02-P4`, `BoardType 4196` | ✅ `GET /System/DeviceInfo` |
| NVR, SoC | Ingenic A1 (ChipID `INGENICA1…`) | ✅ |
| NVR, firmware | `6.9.2-20251226` (hardware y software). UI web `25.08.13.12613` | ✅ |
| NVR, capacidad | 10 canales de vídeo, 10 de audio, `TwoWayAudio=true`, 1 salida de monitor, sin entradas ni salidas de alarma | ✅ |
| Disco | Seagate ST3500418AS, 500 GB, formateado el 2026-10-27; grabación continua (24/7, flujo principal) | ✅ `GET /Disk`, `GetRecordInfoByDay` |
| Cámaras registradas | **1** (canal 1). Canales 2–10 vacíos | ✅ `GET /System/FrontConnectDevicetList` |
| Cámara 1, modelo | `W3CA-52PHM-S/W/BT`, firmware `1.8.1-20260112` | ✅ |
| Cámara 1, funciones | Pan/tilt motorizado, zoom (hasta al menos 6.8X), presets, luz IR + blanca ("inteligente"), micrófono (G.711U) | ✅ Probado (test-log T20–T23) |
| Cámara 1, zoom óptico o digital | Sin determinar | ❓ |
| Cámara 1, altavoz | `TwoWayAudio=true` en el NVR; no probado | ❓ |
| Cámaras 2–4 | No registradas en el NVR | ❓ ¿Están instaladas? |

## Red

| Dato | Valor | Estado |
|---|---|---|
| NVR | `192.168.0.227/24`, gateway `192.168.0.1`, DHCP activo | ✅ |
| MAC del NVR | `5a:5a:02:5b:e4:18` (administrada localmente; no hay OUI de fabricante) | ✅ |
| DNS del NVR | `1.1.1.1`, `8.8.8.8` | ✅ |
| NTP | `pool.ntp.org`, zona `CST-08:00`, última sincronización fallida | ✅ |
| Cámara 1 | `192.168.0.14`, MAC `5a:5a:02:83:97:81`, protocolo privado P6S (`ProtocolType=3`) al puerto 6060 | ✅ |
| Segmento PoE | **Aislado de la LAN**: la cámara usa direcciones del rango de la LAN, pero no responde a ARP ni a ping desde ella | ✅ |
| P2P | NVR: TUTK habilitado, `internet-ready`. Cámara: TUTK habilitado, `offline` | ✅ |
| Baphomet | `192.168.0.8` (estática), Ubuntu 24.04.5, Podman 4.9.3 | ✅ |

## Servicios del NVR

| Puerto | Servicio | Estado |
|---|---|---|
| 80/tcp | HTTP: estáticos `Boa/0.94.14rc21`, API XML `CGI/1.6.3` con Basic auth, WebSocket `/WebSocket/Connect` | ✅ |
| 554/tcp | RTSP `RtpRtspFlyer` (Digest `realm="turing"` y Basic) | ✅ |
| 6060/tcp | Protocolo privado P6S, comandos (`Port/Command`) | ✅ Abierto; protocolo sin documentar |
| 6066/tcp | Protocolo privado P6S, medios (`Port/Media`) | ✅ Abierto; protocolo sin documentar |
| 8885/tcp, 9000/tcp | Desconocidos: aceptan conexión, sin banner ni HTTP | ❓ |
| 3702/udp, 5555/udp, 32108/udp | `open\|filtered`. 3702 no responde a WS-Discovery; 5555 es `Port/Discovery`; 32108 se asocia a P2P (hipótesis) | 🟡 |
| ONVIF | **No disponible** | ✅ Descartado (test-log T10–T11) |

## Flujos

| Ruta | Contenido | Estado |
|---|---|---|
| `/live/0/MAIN` | HEVC 2304×1296 a 10 fps, VBR de 1024 kbps, GOP 15, más PCMU 8 kHz | ✅ |
| `/live/0/SUB` | HEVC 800×448 a 10 fps, más PCMU 8 kHz | ✅ |
| `/live/1/*` | Mismo encuadre que `/live/0` (canal 1) | ✅ |
| `/live/2/*`, `/live/3/*` | Sin flujo (no hay cámaras) | ✅ |
| Mapeo `/live/N` con varias cámaras | `/live/0` es alias del canal 1; falta saber si `/live/2` sería el canal 2 | 🟡 Pendiente de más cámaras |
| Captura JPEG | `GET /Snapshot/1/RemoteImageCapture?ImageFormat=2`: 800×448 | ✅ |

## Cómo reproducir

```bash
set -a; . ~/.config/open-nvr/env; set +a      # NVR_HOST, NVR_USER, NVR_PASS (modo 600)
curl -s -u "$NVR_USER:$NVR_PASS" "http://$NVR_HOST/System/DeviceInfo"
curl -s -u "$NVR_USER:$NVR_PASS" "http://$NVR_HOST/System/FrontConnectDevicetList"
tools/rtsp_describe.py "rtsp://$NVR_HOST:554/live/0/MAIN"
```

El resto de comandos y resultados están en [test-log.md](test-log.md).

## Leyenda

✅ comprobado · 🟡 hipótesis · ❓ pendiente.
