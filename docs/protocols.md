# Protocolos: qué hay, qué podría haber y cómo comprobarlo

Este documento separa tres cosas:

- ✅ **Comprobado** en el NVR de este proyecto.
- 📄 **Documentado**: lo dice una especificación o la documentación de un
  proyecto, pero no se ha probado en este NVR.
- 🟡 **Hipótesis**: inferencia sin fuente ni prueba.

Investigación web hecha el 2026-10-26 sin contactar con el NVR. Las fuentes
están enlazadas en cada apartado.

## Resumen

| Función | Mecanismo real en este NVR | Estado |
|---|---|---|
| Vídeo y audio en directo | RTSP `/live/0/{MAIN,SUB}` | ✅ |
| Descubrimiento | ONVIF/WS-Discovery **no disponibles**. Inventario por `GET /System/DeviceInfo` y `/System/FrontConnectDevicetList` | ✅ |
| Pan/tilt, zoom, foco, iris, presets | API HTTP propietaria `PUT /PTZ/{ch}/{cmd}` (EasyVideo usa la misma) | ✅ Probado |
| Iluminación IR/blanca | `PUT /PTZ/1/Light`; configuración en `/Images/{ch}/IrCutFilter` | ✅ Probado |
| Captura JPEG | `GET /Snapshot/1/RemoteImageCapture?ImageFormat=2` | ✅ |
| Eventos | WebSocket `/WebSocket/Connect` (`fun=event`) | ✅ Conecta; movimiento desactivado |
| Audio bidireccional | Protocolo privado P6S: `VoiceStream*` en 6060 y μ-law de 320 B/40 ms en 6066 ([p6s-protocol.md](p6s-protocol.md)). El backchannel RTSP se ignora | ✅ WebSocket web y AudioWorklet validados con PCM real; botón mantener Hablar y cierre correctos |
| Grabaciones | Búsqueda por HTTP (`/Playback/GetRecordInfoBy*`), reproducción por P6S `HistoryStream*` | ✅ Reproducción web H.264 con pausa, seek, reconexión y cierre; descarga pendiente |
| Configuración | API HTTP/XML (ver [api-reference.md](api-reference.md)) | ✅ Documentada |

> Actualizado el 2026-10-29 con los resultados de las etapas 1 y 2 y el análisis estático de EasyVideo
> ([test-log.md](test-log.md)). Las secciones siguientes conservan la
> investigación de la etapa 0: lo que dice el estándar ONVIF, que este equipo
> no implementa.

## RTSP (comprobado)

✅ Servidor `RtpRtspFlyer` en el puerto 554. Rutas `/live/0/MAIN` (HEVC
2304 × 1296 + PCMU) y `/live/0/SUB`. Reproducen con mpv y FFmpeg.

🟡 El `0` de la ruta probablemente es el índice del canal, así que las demás
cámaras estarían en `/live/1/…`, `/live/2/…` y `/live/3/…`. Se comprueba con
la prueba T03 del [plan](plan.md).

📄 Ni el banner `RtpRtspFlyer` ni la ruta `/live/0/MAIN` aparecen en fuentes
públicas: no figuran en el diccionario de rutas RTSP de Nmap
([rtsp-urls.txt](https://github.com/nmap/nmap/blob/master/nselib/data/rtsp-urls.txt))
ni en guías de URLs de cámaras. Las rutas conocidas de otras familias OEM
(Juan `/chN_M.264`, Xiongmai/DVRIP en el puerto 34567, EseeCloud
`/livestream/N`) no coinciden con este equipo.

## ONVIF

### Descubrimiento y servicios

📄 ONVIF anuncia los dispositivos con WS-Discovery: el cliente envía un `Probe`
por multicast a `239.255.255.250:3702/udp` y los equipos responden con
`ProbeMatches` de tipo `dn:NetworkVideoTransmitter`
([ONVIF Core](https://www.onvif.org/specs/core/ONVIF-Core-Specification.pdf),
[WS-Discovery](https://docs.oasis-open.org/ws-dd/discovery/1.1/os/wsdd-discovery-1.1-spec-os.html)).

📄 El endpoint habitual es `http://<NVR_IP>/onvif/device_service`, aunque el
puerto y la ruta los indica el propio dispositivo en el `ProbeMatch` o en
`GetServices`.

📄 En un NVR multicanal cada canal suele ser un *Media Profile* propio:
`GetProfiles` lista los perfiles y `GetStreamUri(ProfileToken)` devuelve su URL
RTSP. PTZ se direcciona por `ProfileToken` e Imaging por `VideoSourceToken`
([Media Service](https://www.onvif.org/specs/srv/media/ONVIF-Media-Service-Spec.pdf)).
Si el NVR no reenvía PTZ de las cámaras, puede que haya que hablar con las
cámaras directamente, y eso solo es posible si son accesibles desde la LAN.

### Llamadas de solo lectura y llamadas que cambian estado

| Servicio | Solo lectura (seguras para descubrimiento) | Cambian estado (requieren aprobación) |
|---|---|---|
| Device | `GetSystemDateAndTime`, `GetDeviceInformation`, `GetCapabilities`, `GetServices`, `GetScopes`, `GetNetworkInterfaces` | `SetSystemDateAndTime`, `SetNetworkInterfaces`, `CreateUsers`, `SetUser`, `SystemReboot`, `SetSystemFactoryDefault`, `UpgradeSystemFirmware` |
| Media | `GetProfiles`, `GetStreamUri`, `GetSnapshotUri`, `GetAudioOutputs`, `GetAudioOutputConfigurations`, `GetAudioDecoderConfigurations` | `CreateProfile`, `DeleteProfile`, `SetVideoEncoderConfiguration`, `SetAudioOutputConfiguration` |
| PTZ | `GetNodes`, `GetConfigurations`, `GetConfigurationOptions`, `GetStatus`, `GetPresets` | `ContinuousMove`, `RelativeMove`, `AbsoluteMove`, `Stop`, `GotoPreset`, `SetPreset`, `RemovePreset`, `SetConfiguration` |
| Imaging | `GetImagingSettings`, `GetOptions`, `GetMoveOptions`, `GetStatus` | `SetImagingSettings`, `Move`, `Stop` |

Fuentes: [PTZ Service](https://www.onvif.org/specs/srv/ptz/ONVIF-PTZ-Service-Spec.pdf)
(clases de acceso `READ_MEDIA` y `ACTUATE`),
[Imaging Service](https://www.onvif.org/specs/srv/img/ONVIF-Imaging-Service-Spec.pdf).

Plantillas de solo lectura con `onvif-cli` (onvif-zeep). No se han ejecutado
todavía; el puerto `80` está por confirmar (T11/T12 del plan):

```bash
ONVIF="--user $NVR_USER --password $NVR_PASS --host 192.168.0.227 --port 80"
onvif-cli devicemgmt GetDeviceInformation $ONVIF
onvif-cli devicemgmt GetCapabilities "{'Category':'All'}" $ONVIF
onvif-cli media GetProfiles $ONVIF
onvif-cli media GetStreamUri "{'StreamSetup':{'Stream':'RTP-Unicast','Transport':{'Protocol':'RTSP'}},'ProfileToken':'<PROFILE_TOKEN>'}" $ONVIF
onvif-cli ptz GetNodes $ONVIF
onvif-cli ptz GetStatus "{'ProfileToken':'<PROFILE_TOKEN>'}" $ONVIF
onvif-cli imaging GetOptions "{'VideoSourceToken':'<VS_TOKEN>'}" $ONVIF
```

> `GetStreamUri` no cambia nada, pero abrir la URL ocupa una conexión. Los NVR
> baratos suelen limitar las sesiones simultáneas del flujo principal.

### PTZ y zoom

📄 El servicio PTZ define `ContinuousMove` (velocidad), `RelativeMove`,
`AbsoluteMove`, `Stop` y presets. El **zoom óptico** es el eje `Zoom` de esas
mismas operaciones; el servicio Imaging solo mueve el enfoque. Si `GetNodes`
no anuncia un espacio de coordenadas, la operación correspondiente no existe.

🟡 Nada indica todavía que las cámaras tengan motores. Cámaras PoE de bala o
domo de 3 MP en kits de este precio suelen tener lente fija; en ese caso
"zoom" solo puede ser digital, en el cliente.

### Audio bidireccional (backchannel)

📄 Lo define la
[ONVIF Streaming Spec](https://www.onvif.org/specs/stream/ONVIF-Streaming-Spec.pdf)
y forma parte de Profile T. El cliente añade
`Require: www.onvif.org/ver20/backchannel` al `DESCRIBE`; si el servidor lo
soporta, el SDP incluye una pista de audio extra marcada `a=sendonly` por la
que el cliente envía audio (normalmente G.711 PCMU/PCMA o AAC). Si no lo
soporta, responde `551 Option not supported`
([ejemplo](https://github.com/koush/scrypted/discussions/1990)).

📄 La configuración ONVIF correspondiente es `AudioOutputConfiguration` +
`AudioDecoderConfiguration` dentro del Media Profile.

📄 go2rtc implementa este backchannel para fuentes `rtsp://` y lo expone al
navegador por WebRTC ([go2rtc](https://github.com/AlexxIT/go2rtc),
[RTSP en go2rtc](https://go2rtc.org/internal/rtsp/)).

🟡 FFmpeg no parece permitir el backchannel ONVIF: no hay fuente pública que lo
documente. Para hablar a la cámara, go2rtc es la vía práctica.

❓ Para que funcione se necesitan tres cosas, ninguna confirmada: un altavoz en
la cámara, soporte de backchannel en `RtpRtspFlyer` y que el NVR reenvíe el
audio a la cámara.

## Protocolo propietario EasyVideo

❓ El cliente oficial de Windows probablemente usa un protocolo propio (puerto
y formato desconocidos). Analizarlo implicaría capturar el tráfico del cliente
oficial con el permiso del propietario. Queda fuera de esta etapa; solo se
abordará si ONVIF no cubre PTZ o audio (etapa 5 del [plan](plan.md)).

## Fabricante y documentación pública

Resultado de la investigación del 2026-10-26:

- 📄 2NLF es la marca de Frontier Explorer Wu Technology (Shenzhen) Co., Ltd.,
  orientada a Latinoamérica ([about us](https://www.2nlf.com/en/content/4-about-us)).
- 📄 La web de 2NLF no menciona el **GAD3035WT2** ni **EasyVideo**. El
  [mapa del sitio](https://www.2nlf.com/en/sitemap) incluye otros kits PoE
  (GAD3005WT1, GAD3018WT1). La [página de apps](https://www.2nlf.com/en/content/19-download-app)
  recomienda 390Eyes, EseeCloud e IPC360 Home: la marca usa varios
  ecosistemas OEM, así que no se puede deducir cuál lleva este kit.
- 📄 La [base de datos de productos conformes ONVIF](https://www.onvif.org/conformant-products/)
  no tiene resultados para `GAD3035`, `GAD3035WT2`, `EasyVideo` ni `2NLF`
  (solo coincidencias falsas de modelos Panasonic `…2NLFR…`). **No hay
  declaración pública de conformidad ONVIF**. Muchos equipos OEM implementan
  ONVIF parcialmente sin certificarlo, así que hay que comprobarlo en el
  propio equipo.
- 📄 No se encontró ficha técnica, manual ni declaración de PTZ, zoom óptico o
  audio bidireccional para este SKU. Hay manuales de otros GAD en
  [Manuals+](https://manuals.plus/category/2nlf).
- 📄 `Boa/0.94.14rc21` es un servidor web embebido genérico que usan muchos
  fabricantes. No identifica al OEM
  ([ejemplo de aviso de seguridad en otro equipo](https://www.zeroscience.mk/en/vulnerabilities/ZSL-2019-5509.php)).
- 📄 Ningún proyecto libre (Home Assistant, go2rtc, Frigate, librerías
  Python) menciona este modelo, EasyVideo ni `RtpRtspFlyer`.

**Conclusión:** la documentación pública no responde nada sobre PTZ, zoom ni
audio bidireccional. Hay que averiguarlo con las pruebas no destructivas del
plan.

## Herramientas en Linux (Arch)

| Herramienta | Uso | Paquete | Estado |
|---|---|---|---|
| FFmpeg / ffprobe | Inspeccionar flujos, transcodificar (VAAPI) | `ffmpeg` (extra) | ✅ Instalado y funcionando |
| mpv | Visualización nativa de baja latencia | `mpv` (extra) | ✅ Instalado y funcionando |
| curl | Peticiones SOAP/ONVIF y RTSP `DESCRIBE` manuales | `curl` (core) | 📄 |
| go2rtc | Proxy RTSP → WebRTC/MSE, backchannel de audio | AUR `go2rtc` | 📄 |
| MediaMTX | Proxy/relay RTSP sin ONVIF | AUR `mediamtx` | 📄 Alternativa a go2rtc |
| onvif-zeep (`onvif-cli`) | Cliente ONVIF en Python | AUR `python-onvif-zeep` (huérfano) o venv con `pip` | 📄 |
| onvif2-zeep | Igual, con Media2 (HEVC en ONVIF) | `pip` en venv | 📄 |
| libonvif (`onvif-util`, `onvif-gui`) | Descubrimiento WS-Discovery y consultas | AUR `onvif-util`, `onvif-gui` | 📄 |
| OnvifDeviceManager (Quedale) | Alternativa GTK a ONVIF Device Manager | compilar desde [GitHub](https://github.com/Quedale/OnvifDeviceManager) | 📄 |
| Frigate | NVR con detección de objetos | Docker (sin paquete Arch) | 📄 Opcional, etapa futura |

Fuentes: [go2rtc](https://github.com/AlexxIT/go2rtc),
[MediaMTX](https://github.com/bluenviron/mediamtx),
[onvif-zeep](https://pypi.org/project/onvif-zeep/),
[onvif2-zeep](https://pypi.org/project/onvif2-zeep/),
[libonvif](https://github.com/sr99622/libonvif),
[Frigate](https://docs.frigate.video/configuration/live/),
[Arch Wiki: aceleración de vídeo](https://wiki.archlinux.org/title/Hardware_video_acceleration).

> `wsdd` (repos oficiales) implementa WS-Discovery para compartir archivos en
> red Windows/SMB, no para ONVIF.

## HEVC en el navegador

📄 La reproducción de HEVC en navegadores es limitada:

- Chrome/Edge en Linux solo decodifican HEVC por hardware (VA-API). Sin GPU
  compatible no hay imagen.
- Según go2rtc, HEVC por WebRTC funciona en Chrome 136+ y Safari 18+, y por MSE
  solo en Safari. Firefox solo admite H.264 en ambos.

Opciones, de menor a mayor coste:

1. **mpv** para ver en el escritorio: decodifica HEVC sin problemas.
2. **go2rtc** sirviendo HEVC tal cual a navegadores que lo admitan.
3. **Transcodificar a H.264** con VAAPI solo el flujo SUB (o el que se esté
   viendo), con `ffmpeg:` en go2rtc (`#video=h264#hardware`). Comprobar
   antes con `vainfo` (paquete `libva-utils`) que la GPU codifica H.264.
