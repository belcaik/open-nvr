# Plan de integración por etapas

> **Estado a 2026-10-27.** Etapas 1 y 2 ejecutadas ([test-log.md](test-log.md)).
> El punto de decisión de la etapa 2 se resolvió así: **no hay ONVIF**, y PTZ
> y luz funcionan por la API HTTP propietaria. Auditoría en
> [security.md](security.md). Monitoreo implementado en `deploy/baphomet/`
> ([monitoring.md](monitoring.md)) y pendiente de desplegar. La etapa 5
> (protocolo P6S) queda abierta solo para el audio bidireccional y la
> reproducción de grabaciones.

Cada etapa termina con un entregable y actualiza el estado de
[hardware.md](hardware.md) y [protocols.md](protocols.md): una prueba que pasa
cambia ❓ por ✅ y una que falla deja el resultado anotado.

## Clasificación de las pruebas

| Clase | Significado | Quién la autoriza |
|---|---|---|
| **P** Pasiva | No envía nada al NVR (escucha o analiza datos ya capturados) | Libre |
| **L** Lectura | Envía peticiones que solo leen: `OPTIONS`, `DESCRIBE`, `Get*` de ONVIF, `HEAD` | Libre, con límites (abajo) |
| **A** Actuación | Mueve hardware o emite sonido sin cambiar la configuración guardada (PTZ, enfoque, audio a la cámara) | El propietario, prueba a prueba |
| **C** Cambio | Modifica configuración, credenciales, red o firmware | El propietario, por escrito y con plan de vuelta atrás. Fuera del alcance de este plan |

Límites para las pruebas **L**:

- Una petición cada vez, sin paralelismo ni fuerza bruta.
- Escaneo de puertos solo contra `192.168.0.227`, a ritmo bajo (`nmap -T2`).
- Nunca probar credenciales por diccionario: solo las del propietario.
- Credenciales por variable de entorno (`NVR_USER`, `NVR_PASS`), nunca en
  archivos del repositorio.
- Guardar las salidas en `captures/` (ignorado por git) y revisarlas antes de
  publicar algo: contienen números de serie, MAC y a veces credenciales.

## Etapa 0: documentación (esta entrega)

- [x] README, [hardware.md](hardware.md), [protocols.md](protocols.md) y este plan.
- [x] Investigación de documentación pública del fabricante y de ONVIF.

## Etapa 1: inventario de red y flujos (clases P y L)

Objetivo: saber qué servicios ofrece el NVR y cómo están numerados los canales.

| ID | Clase | Prueba | Comando de referencia | Resultado esperado |
|---|---|---|---|---|
| T01 | L | Puertos TCP abiertos | `nmap -T2 -sT -p- 192.168.0.227` | Lista de puertos; buscar 80, 554, 8000, 8899, 34567 y otros |
| T02 | L | Banners de servicio | `nmap -T2 -sV -p <puertos de T01> 192.168.0.227` | Identificar servicios adicionales (ONVIF, protocolo EasyVideo) |
| T03 | L | Canales 1–3 | `ffprobe` sobre `/live/{1,2,3}/{MAIN,SUB}` (ver abajo) | Confirma o descarta la hipótesis de numeración |
| T04 | L | Parámetros de cada flujo | `ffprobe -show_streams -show_format` | Resolución, fps y códec de MAIN y SUB por canal |
| T05 | L | Sesiones simultáneas | Abrir 1, 2, 4 y 8 clientes `ffprobe` sobre SUB | Número máximo de sesiones antes de que el NVR rechace conexiones |
| T06 | P | Tráfico saliente del NVR | Capturar en el router o con port mirroring durante 24 h; nunca con ARP spoofing | Saber si el NVR contacta servidores P2P/nube |

```bash
for ch in 0 1 2 3; do for s in MAIN SUB; do
  printf '%s/%s: ' "$ch" "$s"
  ffprobe -v error -rtsp_transport tcp -select_streams v:0 \
    -show_entries stream=codec_name,width,height,avg_frame_rate -of csv=p=0 \
    "rtsp://$NVR_USER:$NVR_PASS@192.168.0.227:554/live/$ch/$s" || echo "sin flujo"
done; done
```

Entregable: tabla de canales y puertos completa en `hardware.md`.

## Etapa 2: ONVIF y capacidades (clase L)

Objetivo: saber si hay ONVIF y qué anuncia, sin cambiar nada.

| ID | Clase | Prueba | Herramienta | Resultado esperado |
|---|---|---|---|---|
| T10 | L | WS-Discovery | `onvif-util -a` o un `Probe` SOAP a `239.255.255.250:3702` | `ProbeMatch` con `XAddrs` y scopes, o ninguna respuesta |
| T11 | L | Endpoint sin autenticar | `GetSystemDateAndTime` por `curl` a `/onvif/device_service` | Respuesta SOAP (hay ONVIF) o 404/sin respuesta |
| T12 | L | Identidad y servicios | `GetDeviceInformation`, `GetServices`, `GetCapabilities` | Fabricante/modelo/firmware real (pistas del OEM) y servicios disponibles (PTZ, Imaging, Media2, Events, Recording) |
| T13 | L | Perfiles por canal | `GetProfiles`, `GetStreamUri` por perfil | Mapa `ProfileToken` → canal → URL RTSP |
| T14 | L | PTZ disponible | `ptz GetNodes`, `GetConfigurations`, `GetStatus` por perfil | Espacios Pan/Tilt/Zoom anunciados; si no hay nodos, no hay PTZ ONVIF |
| T15 | L | Enfoque/zoom de lente | `imaging GetOptions`, `GetMoveOptions` | Indica si hay lente motorizada |
| T16 | L | Salida de audio | `media GetAudioOutputs`, `GetAudioOutputConfigurations` | Indica si el dispositivo declara altavoz |
| T17 | L | Backchannel RTSP | `DESCRIBE` con `Require: www.onvif.org/ver20/backchannel` (script de la etapa) | Pista `a=sendonly` en el SDP, o `551 Option not supported` |

```bash
# T11: no requiere credenciales según la especificación ONVIF
curl -s -m 5 -H 'Content-Type: application/soap+xml; charset=utf-8' \
  -d '<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>
      <GetSystemDateAndTime xmlns="http://www.onvif.org/ver10/device/wsdl"/>
      </s:Body></s:Envelope>' \
  http://192.168.0.227/onvif/device_service
```

Para T12–T16 se usa `onvif-cli` (onvif-zeep, en un venv). Los comandos con
placeholders están en [protocols.md](protocols.md#llamadas-de-solo-lectura-y-llamadas-que-cambian-estado).

T17 necesita una herramienta propia pequeña: ni `ffprobe` ni la CLI de `curl`
permiten enviar un `DESCRIBE` RTSP con cabeceras propias. Se escribe
`tools/rtsp_describe.py` (Python sin dependencias, autenticación digest) que
solo envía `DESCRIBE` y muestra el SDP.

Entregable: `docs/onvif-capabilities.md` con las respuestas (sin números de
serie) y la columna "Estado" de `protocols.md` actualizada.

**Punto de decisión:**

- Hay ONVIF con PTZ/audio → etapa 3 por ONVIF.
- Hay ONVIF sin PTZ/audio, o no hay ONVIF → probablemente el hardware no lo
  tiene; si hay indicios de lo contrario (por ejemplo, el cliente EasyVideo
  muestra controles PTZ), se valora la etapa 5.

## Etapa 3: actuación controlada (clase A, aprobación por prueba)

Solo si la etapa 2 detecta las capacidades. Cada prueba se ejecuta con el
propietario delante de la cámara.

| ID | Prueba | Garantía de seguridad |
|---|---|---|
| T20 | `ContinuousMove` de pan a velocidad baja durante 0,5 s y luego `Stop` | `Stop` siempre en un `finally`; anotar posición inicial con `GetStatus` |
| T21 | `RelativeMove` de zoom +10 % y vuelta al valor inicial | Restaurar desde `GetStatus` previo |
| T22 | `GotoPreset` a un preset existente | No crear ni borrar presets |
| T23 | Backchannel: enviar 2 s de tono de 1 kHz en PCMU a 8 kHz con go2rtc | Volumen bajo; una cámara cada vez |

Entregable: `docs/ptz-audio.md` con lo que funciona y la latencia observada.

## Etapa 4: arquitectura local mínima

Objetivo: ver y administrar las cuatro cámaras desde Arch sin Windows.

```
              LAN 192.168.0.0/24
┌──────────────┐  RTSP :554    ┌───────────────────────────────┐
│ NVR EasyVideo├──────────────►│ go2rtc (servicio systemd)     │
│ 192.168.0.227│  ONVIF (¿?)   │  - 1 conexión al NVR por flujo│
│  └ 4 cámaras │◄──────────────┤  - restream RTSP :8554        │
└──────────────┘  backchannel  │  - WebRTC/MSE :1984           │
                               │  - transcodificación VAAPI    │
                               │    opcional (solo SUB)        │
                               └──┬──────────┬─────────────┬───┘
                                  │          │             │
                          mpv (mosaico)  navegador    open-nvr CLI
                          vía :8554      (LAN/WireGuard) (Python: ONVIF
                                                         PTZ/estado)
```

Decisiones:

1. **go2rtc como único consumidor del NVR.** El NVR probablemente limita las
   sesiones (T05). go2rtc abre una conexión por flujo y la reparte a todos
   los clientes. Además soporta HEVC, PCMU y backchannel ONVIF. MediaMTX es la
   alternativa si el backchannel no hace falta.
2. **mpv como visor principal.** Decodifica HEVC con VA-API sin transcodificar.
   Un script lanza el mosaico 2×2 con los cuatro SUB y abre el MAIN de una
   cámara a demanda.
3. **Navegador como visor secundario.** HEVC directo donde funcione; si no,
   H.264 transcodificado solo del flujo SUB.
4. **CLI `open-nvr` en Python** sobre la API HTTP propietaria (no ONVIF, que
   el NVR no ofrece): `status`, `channels`, `ptz`, `preset`, `light`,
   `snapshot`. Lectura por defecto; las órdenes de actuación piden
   confirmación. Pendiente.
5. **Grabación:** el NVR sigue siendo el grabador principal. Una copia local
   (go2rtc + `ffmpeg -c copy` en segmentos, o Frigate) queda para una etapa
   posterior.
6. **Acceso remoto:** solo con WireGuard hacia el host Linux. Nunca reenviar
   puertos del NVR en el router.
7. **Configuración:** `config/go2rtc.yaml` con credenciales tomadas de
   variables de entorno (`${NVR_PASS}`), no versionadas.

Entregables: `config/go2rtc.yaml.example`, unidad systemd de usuario,
`scripts/mosaic.sh` y CLI mínima con `status` y `channels`.

## Etapa 5: protocolo EasyVideo (opcional, decidir tras la etapa 2)

Solo si ONVIF no cubre una función que el hardware sí tiene. Implica capturar
el tráfico del cliente oficial de Windows (en una VM, con permiso del
propietario) y documentar el protocolo. Es ingeniería inversa de un equipo
propio para interoperabilidad. Requiere aprobación específica antes de empezar.

## Etapa 6: endurecimiento (clase C, aprobación por escrito)

Propuestas que cambian el NVR o la red. **No se ejecutan sin aprobación
explícita:**

- Contraseña única y robusta, y un usuario de solo visualización para go2rtc.
- Bloquear la salida a Internet del NVR en el router si T06 confirma tráfico
  P2P y no se usa la app móvil.
- Reservar la IP `192.168.0.227` en el DHCP.
- Comprobar si hay firmware nuevo, pero no instalarlo sin una copia de la
  configuración y una vía de recuperación documentada.
