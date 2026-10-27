# Protocolo P6S (puertos 6060 y 6066)

Lo que se sabe del protocolo privado que usa el cliente EasyVideo. Se obtuvo
**solo por observación pasiva**: el tráfico del cliente oficial en una VM
Windows (`captures/vm/win10-easyvideo.pcap`, 873 s, 2026-10-27), capturado
con `tools/vm_capture.sh` y analizado sin enviar nada al NVR.

Nivel de certeza: ✅ verificado en la captura · 🟡 hipótesis.

## Qué usa P6S y qué usa HTTP

EasyVideo no tiene interfaz propia: **carga la UI web del NVR**
(`index.html` y sus JS desde el puerto 80) y le añade un puente local
(`ws://127.0.0.1:32858`) que habla P6S. Por eso:

| Función | Transporte | Estado |
|---|---|---|
| Configuración, OSD, imagen, red, hora | HTTP `/…` (Basic auth) | ✅ La misma API de [api-reference.md](api-reference.md) |
| PTZ, zoom, foco, iris, luz, limpiaparabrisas | HTTP `PUT /PTZ/{ch}/{cmd}` | ✅ Comandos vistos: `TurnUp`, `TurnUpRight`, `TurnDown`, `TurnRight`, `AutoLeftRight`, `ZoomIn/Out`, `FocusNear`, `ApertureBigger/Smaller`, `Light`, `RainBrush` |
| Vídeo en directo | P6S: comandos en 6060, medios en 6066 | ✅ |
| **Audio bidireccional** | P6S: `VoiceStream*` en 6060, audio en 6066 | ✅ |
| Búsqueda de grabaciones | HTTP `GET /Playback/GetRecordInfoByMonth` | ✅ |
| Reproducción de grabaciones | No apareció en la captura (no llegó a iniciarse) | ❓ |
| Eventos | WebSocket `ws://NVR/WebSocket/Connect` | ✅ Una conexión abierta |

## Canal de comandos (6060/tcp)

Una conexión TCP persistente. Cada mensaje es una **cabecera binaria de 36
bytes** seguida de un **documento XML** en GB2312:

```
00  05 01 11 54            magic (comandos)
04  00 00 10 01            versión (constante)
08  xx xx xx xx            número de secuencia o nonce (varía en los latidos)
0C  00 00 00 00
10  00 00 00 LL            longitud del XML (BE) = tamaño del mensaje − 36   ✅
14  00 00 00 03            tipo (constante en esta captura)
18  12 bytes a cero
24  <?xml version="1.0" encoding="GB2312" standalone="yes" ?>
    <Command ID="n"> <Elemento atributos…/> </Command>
```

El `ID` de las respuestas es el de la petición + 1.

| Petición | Respuesta | Significado |
|---|---|---|
| `LoginGetFlag` (26) | `LoginGetFlagReply LoginFlag="<entero>" Ret="-1"` (27) | Reto de login |
| `UserLogin UserName UserIP UserMAC LoginFlag="<32 hex>"` (24) | `UserLoginReply CmdReply="0" TCPPort="6066" RTPPort="554" CmdPort="6060" LoginFlagClient=…` y lista de capacidades (25) | Login. El hash depende del reto; **la fórmula exacta no se ha determinado**: no es MD5 simple de contraseña, reto y usuario en ningún orden probado. 🟡 |
| `HeartBeatNotice` (78, lo envía el NVR cada 3 s) | `HeartBeatNoticeReply CmdReply="0" NetDataFlow="0" …` (79) | Mantener la sesión |
| `RealStreamCreateRequest Channel="0" Mode="1" Type="4"` (136) | `RealStreamCreateReply Result="0" MediaSession="<id>"` (137) | Crear flujo en directo. `Channel` es base 0 |
| `RealStreamStartRequest MediaSession` (138) | `…Reply Result="0"` (139) | Iniciar |
| `RemoteForceIFrame Channel` (106) | `…Reply CmdReply="0"` (107) | Pedir un fotograma clave |
| `RealStreamStopRequest` / `RealStreamDestroyRequest` (140/142) | `Result="0"` | Parar y liberar |
| `VoiceStreamCreateRequest Channel="0" VoiceMode="2" TransferMode="1" StreamType="0"` (148) | `VoiceStreamCreateReply MediaFlag="<id>" CmdReply="0"` (149) | Crear sesión de voz con la cámara |
| `VoiceStreamStartRequest MediaSession` (150) | `Result="0"` (151) | Iniciar la voz |
| `VoiceStreamStopRequest` / `VoiceStreamDestroyRequest MediaFlag` (152/154) | `Result="0"` / `CmdReply="1"` | Parar y liberar |

## Canal de medios (6066/tcp)

Una conexión TCP por flujo (38 en la captura). Tramas con **cabecera base de
32 bytes**, una extensión y la carga:

```
00  05 01 11 50            magic (medios)
04  00 00 10 01            versión
08  EE EE EE EE            longitud de la extensión (BE)     ✅
0C  PP PP PP PP            longitud de la carga (BE)         ✅
10  00 00 00 03            tipo
14  12 bytes a cero
20  extensión (EE bytes)
..  carga (PP bytes)
```

Validado recorriendo la sesión de voz completa: 604 tramas del cliente y 680
del NVR, terminadas sin desincronizarse.

- **Primera trama de cada conexión**: extensión de 4 bytes con el
  `MediaSession` (o `MediaFlag`) en BE y carga vacía. Enlaza la conexión de
  medios con la sesión creada en 6060 (por ejemplo, `77 67 a6 94` =
  2003281556). ✅
- **Audio** (en ambos sentidos): extensión de 44 bytes y carga de 320 bytes
  de **G.711 μ-law a 8 kHz, es decir, 40 ms por trama**. 603 tramas son
  24,1 s, que coincide con la prueba de voz (01:37:10–01:37:36). ✅
- En las tramas del NVR, la extensión lleva además un valor creciente (marca
  de tiempo 🟡) y repite la longitud `0x140`.
- El vídeo usa el mismo encuadre con otras extensiones y tamaños; no se ha
  diseccionado (el vídeo ya se obtiene por RTSP).

## Qué falta para implementar el audio desde Linux

1. **La fórmula del hash de `UserLogin`.** Sin ella no se puede abrir una
   sesión P6S. Vías: analizar estáticamente el ejecutable de EasyVideo (un
   cliente propio, con fines de interoperabilidad) o capturar varios logins
   para comparar.
2. Con el login resuelto, el flujo es: `LoginGetFlag`, `UserLogin`,
   `VoiceStreamCreateRequest`, abrir 6066, enviar la trama de enlace con
   `MediaFlag`, `VoiceStreamStartRequest` y tramas de 320 bytes μ-law cada
   40 ms. Para salir, `VoiceStreamStop` y `VoiceStreamDestroy`.
3. No se enviará al NVR ninguna trama cuyo formato no esté verificado en esta
   captura.

## Tráfico hacia Internet del cliente

| Destino | Contenido | Observación |
|---|---|---|
| `utilerp.p6sai.com` → `112.74.43.252:8090` | `GET /UtilERP/device/CloudUpgradeFirmwareList?…&customer=C009&deviceType=FFF7&callbackName=EasyVideo&curVersion=2.4.0-19700101` | Consulta de actualizaciones **en HTTP sin cifrar**. Revela el código de cliente OEM (`C009`) |
| `obs-a635-cloud-upgrade.obs.cn-south-1.myhuaweicloud.com` → `122.9.127.165:443` | TLS, pocos bytes | Bucket de actualizaciones en Huawei Cloud (Cantón) |
| Resto | Microsoft, Bing, Edge y webs visitadas | Tráfico propio de Windows y del navegador, no del cliente |

No se vio tráfico P2P (TUTK) del cliente: con acceso por IP no lo usa.
