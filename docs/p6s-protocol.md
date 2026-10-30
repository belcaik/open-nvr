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
| `UserLogin UserName UserIP UserMAC LoginFlag="<32 hex>"` (24) | `UserLoginReply CmdReply="0" TCPPort="6066" RTPPort="554" CmdPort="6060" LoginFlagClient=…` y lista de capacidades (25) | Login. `LoginFlag` se deriva con la variante DES/3DES documentada abajo. **Verificado el 2026-10-29 con el cliente Go contra el DVR real** |
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

## Login P6S recuperado y validado

Análisis estático delegado (`rea-login-talk`, Codex; revisión independiente de
Kimi) de `VideoNetClient.dll` SHA-256
`b9c9c0b6f3c377dfd31e6c5e78a869355fd83a09658eefd1e63b7bad8af49a9c`. El DLL
instalado en la VM tiene el mismo hash. Después se instrumentó el login oficial
con Frida y se comprobó que `VideoNetClient_UserLogin`, `CUser::Login` y
`CTripleDES::Encode` reciben exactamente los mismos bytes de contraseña. El
plaintext observado fue `63daa3fe    7129`, que confirma el formato `%8x%8x`
sin exponer la credencial ni el criptograma (evidencia
`ev_31d5fbf1236219448f066b37ccc518be9af65882f4e4df0d5379b52bc017b862`).

`LoginFlag` no usa MD5. Para el reto entero `F` de `LoginGetFlagReply` y un
entero aleatorio `R` entre 1 y 32767:

```text
P = ASCII(sprintf("%8x", uint32(F + 177))) ||
    ASCII(sprintf("%8x", R))                 // 16 bytes; espacios a la izquierda
K = password truncada/rellenada con NUL a 8 bytes (DES) o 16 bytes (3DES)

si len(password) <= 8:
    C = DESdll-ECB-Encrypt(K, P)
si 8 < len(password):
    C = 3DESdll-EDE-ECB(K1, K2, K1, P)       // dos claves independientes

C = swap_adjacent_bits_por_byte(C)
LoginFlag = hex_minúsculo(C)                  // 32 caracteres
```

`DESdll` usa las tablas DES canónicas y empaqueta bits LSB-first, pero cada
S-box emite también sus cuatro bits LSB-first antes de la permutación P. Por
ello no basta rodear `crypto/des` con inversiones de bits: esa aproximación no
reproduce las rondas. La permutación final adicional es:

```text
swap_adjacent_bits(x) = ((x & 0x55) << 1) | ((x & 0xaa) >> 1)
                        b0↔b1, b2↔b3, b4↔b5, b6↔b7
```

La necesidad de `swap_adjacent_bits` se comprobó contra dos logins oficiales,
con igualdad de los 128 bits en ambos; no se afirma haberla localizado en el
código estático del DLL. La salida S-box LSB-first sí está en `0x10012d80`.
`CUser::Login` está en `0x10070100` y `CTripleDES::Encode` en `0x10012620`.

Vectores ya verificados: password `password`, `F=0`, `R=0x1234` →
`edcb9a37dd8b642567bb2a23bb3bd15d`; password `password1234`, mismos valores →
`5a279a309cc6cdf8c8fc822ef97b7623`. Otro vector independiente es
`example-pass-123`, `F=0x12345678`, `R=0x1234` →
`66abe94e99ebf736e0e8872beb021d34`.

Una respuesta correcta debe cumplir `LoginFlagClient == R+1`. La cabecera de
comandos y los XML usan longitudes en bytes; el cuerpo se codifica en GB2312.
El 2026-10-29 el probe Go real completó `LoginGetFlag` y `UserLogin` con
`CmdReply=0`, puertos 6060/6066/554 y 1607 bytes de capacidades. Antes se
recibía `CmdReply=22` con las mismas credenciales HTTP válidas; la causa era la
serialización incompatible del `LoginFlag`, no una contraseña incorrecta.

El firmware inserta entre las capacidades una etiqueta sin nombre:
`< Major="-1" Minor="-1" Revision="-1" />`. No es XML válido ni identifica una
capacidad aprovechable. El parser elimina únicamente ese patrón concreto y
conserva las válidas; cualquier otra malformación sigue rechazada.

Las capacidades declaradas por el firmware en la respuesta oficial son:
`RemoteReboot`, `ForceIFrame`, `ClearAlarm`, `ImageCapture`, `FileLock`,
`RecordControl`, `AlarmoutControl`, `PtzControl`, `DataExistCheck`,
`HistoryQuery`, `FileQuery`, `LogQuery`, `FileUpload`, `FileDownload`,
`TransparentChannel`, `EventNotice`, `HistoryChannel`, `RealtimeView`,
`VoiceTalk`, `DiskGroupManage`, `GetDataSize`, `HistoryQueryMultiType`,
`HistoryChannelMultiType`, `DiskGroupManageMultiType`, `SetChromaDefault`,
`MultiFramesSessionSend` y `EventNoticeEx`. Declarar una capacidad no demuestra
que su implementación esté ya integrada en open-nvr ni que todos sus campos
sean conocidos.

## Funciones P6S aún por investigar

- **Vídeo histórico:** significado de la extensión de 44 bytes, timestamps y fin
  de clip. El parser MPEG-PS está implementado y probado con fixtures, pero la
  sesión Go real devolvió vídeo elemental `b7` y no ejercitó pack/PES ni PTS/DTS.
- **Descarga y consultas:** falta una captura completa con `FileType` y
  `FileName`, y capturas runtime de `HistoryQuery*`, `FileQuery`, `LogQuery`,
  `GetDataSize` y `HistoryQueryMultiType`.
- **Archivos y disco:** `FileUpload*`, `FileLock`, `DataExistCheck`,
  `DiskGroupManage*` y la relación entre descarga histórica y upload.
- **Canales auxiliares:** `TransparentChannel`, `TransparentData*`, `Panel*`,
  `RemoteDesktop*`, `Telnet*` y `DeviceControl*`. Sólo hay contratos estáticos;
  no se ha observado su uso en el DVR y no deben activarse a ciegas.
- **Descubrimiento y configuración:** `DeviceProbe*`, `DetectDeviceList*`,
  `SetChromaDefault`, `SetScreenSplitMode`, `MultiFramesSessionSend` y
  `EventNoticeEx`.
- **Voz:** significado de cada campo de la extensión de 44 bytes. La sesión
  corta desde Go ya está validada.
- **Superficie no identificada:** 8885/tcp y 9000/tcp aceptan conexiones sin
  banner y cierran ante HTTP, `LoginGetFlag` P6S y `OPTIONS` RTSP. Estas pruebas
  acotadas no identifican qué protocolos esperan ni demuestran que hablen P6S.

La presencia de un export en `VideoNetClient.dll` prueba una API del SDK, no que
el firmware la implemente ni que EasyVideo la use. La revisión estática encontró
callsites de `HistoryQueryMultiType` en `EvPlugs.dll`, por lo que ésa es la
siguiente consulta prioritaria para captura; el resto de auxiliares requiere
request/reply, paginación y cierre reales antes de habilitar envíos.

## Audio desde Linux

1. **Hecho:** login Go, `VoiceStreamCreateRequest`, apertura de 6066, enlace con
   `MediaFlag`, `VoiceStreamStartRequest` y tramas de 320 bytes μ-law cada 40 ms.
   El cierre ejecuta `VoiceStreamStop` y `VoiceStreamDestroy`. El probe
   `go run ./captures/p6svoice` envió tres bloques de silencio y completó ese
   cierre contra el DVR, también después de serializar los envíos concurrentes.
2. La captura confirma la extensión de audio de 44 bytes, conservada como fixture
   opaco en `internal/p6s/voice/testdata`; no se inventan offsets ni timestamps.
3. La sesión responde heartbeats, acota colas/timeouts y coordina envío y cierre.
   El audio entrante se drena sin interpretarlo.

## Reproducción histórica y descarga

El análisis estático (`rea-playback-recovery`, Kimi) se contrastó con una
reproducción real del canal D1 el 2026-10-28. Las capturas
`win10-p6s-playback-search.pcap` y `win10-p6s-playback-close.pcap` contienen
búsqueda, reproducción de un intervalo y cierre. El firmware usa la variante
`MultiType`, no la petición simple 124 vista estáticamente:

| Operación | Petición → respuesta | Campos observados |
|---|---|---|
| Crear histórico | `HistoryStreamCreateMultiType` (185) → `HistoryStreamCreateReply` (125) | `Version="-1"`; `HistoryStreamPara DiskGroup="1" Channel="0" EnableEndTime="0" StreamType="63" TransportType="1" StreamMediaType="0" TaskType="1001"`; fechas completas. Respuesta `CmdReply="0"`, `MediaFlag`, `Version` |
| Destruir | `HistoryStreamDestroy` (126) → reply (127) | `Version="-1"`, `MediaFlag`; respuesta `CmdReply="0"` |
| Seek | `HistoryStreamPos` (128) → reply (129) | `Operation="2"`, `MediaFlag`, `StreamFlag="1"`, `PosTime` con fecha/hora |
| Control | `HistoryStreamCtl` (130) → reply (131) | `Operation="1"` inicia/reanuda, `2` pausa/detiene sin destruir, `3` cambia velocidad; `MediaFlag`, `Speed="0"` en la sesión observada |
| Crear descarga | `FileDownloadCreate` (56) → reply (57) | `FileType`, `FileName`; sólo recuperado estáticamente |
| Liberar descarga | `FileDownloadRelease` (59) → reply (60) | `MediaFlag` de descarga; sólo recuperado estáticamente |

Secuencia observada: login existente → `HistoryStreamCreateMultiType` → abrir
6066 y enlazar `MediaFlag` (`EE=4`, `PP=0`) → `HistoryStreamCtl Operation="1"`
→ consumir vídeo → `HistoryStreamPos` y otros controles según corresponda →
`Operation="2"` → `HistoryStreamDestroy`. Las respuestas 129/131 contienen
`Operation` o `Version` sin correspondencia con la petición; se valida
`CmdReply` y se correlaciona por ID.

En 6066, el cliente envía el `MediaFlag` en una trama de enlace de 4 bytes. El
servidor responde primero con otra extensión de 4 bytes y después envía tramas
con extensión de 44 bytes. Las capturas oficiales contienen tanto payloads con
`00 00 01 b7` como packs MPEG-PS `00 00 01 ba`: `b7` se conserva como vídeo
elemental y no se afirma que sea un pack PS. El probe `go run ./captures/p6shistory`
completó create, enlace, start, seek, stop y destroy, y guardó 4.172.197 bytes en
`captures/p6shistory/clip.video`. Esa ejecución fue `elemental-b7`, con cero
packs/PES y sin fin PS; no valida timestamps ni `program_end_code` en red.

La descarga encadena una sesión histórica con `FileDownloadCreate`, otra
conexión 6066 enlazada al `MediaFlag` de descarga y `FileDownloadRelease`. La UI
usa el puente local `Fountion:"download"`, no una URL HTTP del NVR. La captura
`win10-p6s-download.pcap` no contiene `FileDownloadCreate`: el cliente oficial
quedó sin ventana operable y una ventana nueva del navegador pidió autenticación.
Por tanto `FileName`, `FileType` y la descarga end-to-end siguen desconocidos; la
VM quedó suspendida y sin captura activa.

Evidencia REA principal de la investigación:
`ev_a84e83dcada09816de09d5cf24b8cd1ecedae1fbae296b04fe5a479312ef6c02`
(serializadores históricos/descarga) y
`ev_4446e27a744b9f963a7a9477967cfcbeec2d635dc15d304613b6373a8348ba09`
(exports y validaciones del SDK).

## Implementación en open-nvr

- `internal/p6s`: login DES/3DES, framing acotado de comandos y medios, enlace
  `MediaFlag` y login sobre `net.Conn` con heartbeat y cancelación. No hace Dial
  por sí mismo ni incluye credenciales en errores.
- `internal/p6s/history`: comandos `MultiType`, seek capturado y sesión completa
  sobre conexiones inyectadas, con `NetHistoryDataFlow` en heartbeat, cancelación
  y stop/destroy.
- `internal/p6s/mpegps`: extensión opaca, reensamblado de pack/PES, SCR/PTS/DTS
  acotados al paquete actual, `program_end_code`, límites de memoria y pruebas de
  fragmentación. No interpreta todavía la extensión ni el vídeo elemental `b7`.
- `internal/playback`: reproducción web con confirmación de enlace y secuencia
  Stop→Seek→Start→Speed, fuente HTTP temporal HEVC y salida H.264 de go2rtc.
  Retira el prefijo propietario anterior al primer VPS, tolera reconexiones y
  coordina pausa, seek y cierre.
- `internal/p6s/voice` e `internal/talk`: PCM S16LE ↔ PCMU, mensajes `VoiceStream*`,
  extensión capturada opaca y sesiones web con envíos serializados y cierre explícito.
- Pruebas golden, fragmentación byte a byte, `net.Pipe`, concurrencia con `-race`
  y composición entre paquetes. `go test -race ./...`, `go vet ./...` y
  `git diff --check` pasan. Login, voz e histórico están probados contra el DVR;
  descarga y auxiliares no se anuncian como implementados end-to-end.

## Tráfico hacia Internet del cliente

| Destino | Contenido | Observación |
|---|---|---|
| `utilerp.p6sai.com` → `112.74.43.252:8090` | `GET /UtilERP/device/CloudUpgradeFirmwareList?…&customer=C009&deviceType=FFF7&callbackName=EasyVideo&curVersion=2.4.0-19700101` | Consulta de actualizaciones **en HTTP sin cifrar**. Revela el código de cliente OEM (`C009`) |
| `obs-a635-cloud-upgrade.obs.cn-south-1.myhuaweicloud.com` → `122.9.127.165:443` | TLS, pocos bytes | Bucket de actualizaciones en Huawei Cloud (Cantón) |
| Resto | Microsoft, Bing, Edge y webs visitadas | Tráfico propio de Windows y del navegador, no del cliente |

No se vio tráfico P2P (TUTK) del cliente: con acceso por IP no lo usa.
