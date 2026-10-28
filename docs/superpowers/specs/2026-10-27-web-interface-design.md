# Interfaz web propia y migración a Go

Fecha: 2026-10-27 · Estado: aprobado en conversación, pendiente de revisión escrita.

## Objetivo

Ver en directo, escuchar, mover, configurar y revisar las grabaciones de las
cuatro cámaras del NVR 2NLF desde una interfaz web propia servida por
Baphomet, sin EasyVideo ni nube. En el mismo cambio, reescribir en Go los
vigilantes `nvr-watch` y `egress-watch` y eliminar el código Python de
`services/`.

## Requisitos

- Un solo repositorio (`github.com/belcaik/open-nvr`), un solo módulo Go y un
  solo binario `opennvr`.
- Se ejecuta en Baphomet como contenedor y se usa desde el navegador.
- Las grabaciones viven en el disco del NVR. La app no graba ni guarda vídeo.
- Clientes: Chrome, Edge y Firefox en escritorio, y Chrome, Firefox y Brave
  en Android.
- HTTPS en `nvr.belcaik.com` (→ 192.168.0.8) con Caddy y Let's Encrypt por
  DNS-01 de Cloudflare. Solo es accesible desde IP privadas.
- El NVR y las cámaras no se exponen a Internet, y el navegador nunca recibe
  las credenciales del NVR.
- Imágenes ligeras: el binario Go va sobre una base mínima.

## Arquitectura

```
navegador ──HTTPS──▶ Caddy (nvr.belcaik.com, solo IP privadas)
                        │
                        ▼
                  opennvr web ──HTTP API (Basic)──▶ NVR 192.168.0.227
                        │  ▲                          ▲
         señalización / │  │ config generada          │ RTSP (1 por cámara y calidad)
         API interna    ▼  │                          │
                       go2rtc ───────────────────────┘
                        │
                        └──WebRTC UDP/TCP 8555 (192.168.0.8)──▶ navegador
```

- `opennvr web`: login, sesiones, páginas HTML con htmx, control PTZ y de
  configuración mediante la API HTTP del NVR, y proxy de señalización hacia
  go2rtc.
- `go2rtc`: contenedor oficial, versión fija, variante con VA-API y acceso a
  `/dev/dri/renderD128`. Su API (1984) no se publica fuera de la red del
  compose. Solo publica el puerto de medios WebRTC 8555 en 192.168.0.8.
- `opennvr nvr-watch` y `opennvr egress-watch`: el mismo binario con otro
  subcomando y el mismo comportamiento que los servicios Python actuales.

## Estructura del código

```
go.mod                  module github.com/belcaik/open-nvr
cmd/opennvr/main.go     solo enruta subcomandos: web, nvr-watch, egress-watch,
                        hash-password, version
internal/
  config/               carga de entorno y secretos de archivo (*_FILE), redacción
  nvr/                  cliente de la API HTTP del NVR, un archivo por área:
                        client.go (Basic, timeouts, semáforo de 2), device.go,
                        ptz.go, image.go, stream.go, record.go, network.go,
                        time.go, events.go (WebSocket /WebSocket/Connect)
  p6s/                  fase 5; vacío hasta entonces
  media/                genera la config de go2rtc y hace de proxy de señalización
  camera/               dominio: Camera, Channel, Capabilities; une nvr + media
  auth/                 argon2id, sesiones con cookie firmada, CSRF, límite de intentos
  notify/               cliente ntfy
  web/
    server.go           router (net/http ServeMux), middleware
    handlers/           un archivo por página
    templates/          layout y parciales (html/template, embebidos)
    static/             htmx (embebido), app.css, js/video.js, js/ptz.js
  watch/
    nvrwatch/           port de services/nvr-watch
    egresswatch/        port de services/egress-watch
testdata/nvr/           XML reales saneados (sin serie, MAC, UID ni credenciales)
```

Reglas:

- Las dependencias van en una sola dirección:
  `web → camera → nvr | media | p6s`. `nvr` y `p6s` no importan nada de
  `web`.
- Los handlers validan la entrada y llaman a servicios. Las plantillas solo
  muestran datos. No hay lógica de negocio en el JavaScript.
- Dependencias externas permitidas: `github.com/coder/websocket` y
  `golang.org/x/crypto` (argon2). Añadir otra requiere justificarla en el
  plan.

## Vídeo y audio

- Cada cámara N (1–4) tiene dos fuentes en go2rtc, `camN` (`/live/N/MAIN`) y
  `camN_sub` (`/live/N/SUB`), con RTSP por TCP. go2rtc solo conecta al NVR
  mientras haya clientes.
- Transcodificación bajo demanda: `camN_h264` y `camN_sub_h264` usan
  `ffmpeg:camN#video=h264#hardware=vaapi#audio=opus`. Solo funcionan mientras
  alguien las consume.
- Elección en el navegador (`video.js`): si
  `MediaSource.isTypeSupported('video/mp4; codecs="hvc1.1.6.L120.90"')` es
  cierto o WebRTC ofrece H.265, se usa la fuente HEVC. Si no, la H.264. El
  orden de transporte es WebRTC y, si falla, MSE.
- Audio: WebRTC lleva PCMU sin conversión. MSE recibe Opus/AAC transcodificado.
- La cuadrícula usa `SUB` y la vista de una cámara usa `MAIN` con audio.
- El navegador solo habla con `opennvr web`
  (`/api/stream/{cam}/{webrtc|mse}`), que valida la sesión y reenvía a
  go2rtc. Los medios WebRTC van directos al puerto 8555 de 192.168.0.8.

## Funciones por fase

| Fase | Contenido | Criterio de hecho |
|---|---|---|
| 1 | Módulo Go, port de nvr-watch y egress-watch con todos sus tests, imagen `open-nvr` única, CI en Go y eliminación de Python | Tests Go en verde. En Baphomet, los vigilantes con la nueva imagen hacen lo mismo que antes (estado, avisos, contadores) |
| 2 | Login, cuadrícula de 4 cámaras en directo, vista de una cámara con audio, PTZ (8 direcciones y velocidad), zoom, enfoque, presets (ir, guardar, borrar), luz, limpiaparabrisas y captura JPEG | Vídeo y PTZ funcionando en Chrome, Firefox y Android desde `https://nvr.belcaik.com` |
| 3 | Configuración: imagen (brillo, contraste, IR), horario de grabación, hora y NTP, estado de disco y canales, red (solo lectura). Eventos de movimiento en vivo por el WebSocket del NVR | Los cambios se aplican y se leen de vuelta. Los eventos aparecen en la UI |
| 4 | Grabaciones: calendario mensual y línea de tiempo diaria con `GetRecordInfoByMonth/ByDay`. Se investiga la reproducción (RTSP de playback o P6S) antes de construirla | Calendario y línea de tiempo correctos. La reproducción depende del resultado de la investigación |
| 5 | Hablar por las cámaras mediante P6S | Bloqueada hasta conocer la fórmula del login |

Cada fase se planifica e implementa por separado, con su propio plan. Este
documento cubre el diseño de todas, y el primer plan cubre las fases 1 y 2.

## Seguridad

- Un único usuario `admin`. El hash argon2id se genera con
  `opennvr hash-password` y llega como secreto de podman
  (`web_password_hash`). La clave de firma de cookies es otro secreto
  (`web_session_key`, 32 bytes aleatorios).
- Cookie de sesión `HttpOnly`, `Secure`, `SameSite=Strict`, firmada con
  HMAC-SHA256, que caduca tras 7 días sin uso. Las sesiones se guardan en
  memoria: al reiniciar hay que volver a iniciar sesión.
- CSRF: token por sesión en la cabecera `X-CSRF-Token`, que htmx envía con
  `hx-headers`. Se exige en todo método distinto de GET.
- Límite de login: 5 intentos fallidos por IP en 15 minutos.
- Cabeceras: CSP sin scripts en línea (`script-src 'self'`),
  `frame-ancestors 'none'`, `Referrer-Policy: no-referrer`.
- Las credenciales del NVR solo están en el contenedor `web` y en la
  configuración de go2rtc. Esa configuración se escribe en un volumen privado
  con modo 600 y nunca se registra en logs.
- Las operaciones destructivas del NVR (formatear, restablecer, borrar
  grabaciones, telnet, firmware) no se exponen en la UI.

## Despliegue

- **Imagen única** `ghcr.io/belcaik/open-nvr`: binario estático
  (`CGO_ENABLED=0`) sobre `gcr.io/distroless/static` o `scratch` con
  certificados. Desaparecen las imágenes `open-nvr-nvr-watch` y
  `open-nvr-egress-watch`.
- **Compose:** los servicios `nvr-watch` y `egress-watch` usan la imagen
  única con `command:` distinto. Se añaden `web` y `go2rtc`, y las
  comprobaciones de salud se escriben como `CMD-SHELL` (limitación de
  podman-compose 1.0.6). `distroless` no tiene shell, así que el binario
  ofrece `opennvr healthcheck <subcomando>` y se usa la forma `CMD` sin shell.
  Hay que verificar con el entorno de prueba de podman-compose 1.0.6 que esa
  forma no se estropea. Si se estropea, se usa una base `busybox`.
- **Caddy:** CI construye la imagen `ghcr.io/belcaik/open-nvr-caddy` con
  xcaddy y `github.com/caddy-dns/cloudflare`, y sustituye la imagen del Caddy
  actual en `/opt/services/caddy`, que sigue sirviendo `mcp-memory`. El token
  de Cloudflare (permiso Zone.DNS:Edit solo para `belcaik.com`) va como
  secreto. El bloque `nvr.belcaik.com` responde 403 a IP no privadas.
- **Conexión de Caddy con `web`:** primero se intenta que `web` escuche en
  `127.0.0.1:8095` y Caddy llegue por loopback. Si el Caddy sin root no
  alcanza el loopback del host, `web` escucha en `192.168.0.8:8095` y una
  regla nft en `inet opennvr` solo permite a Caddy entrar a ese puerto. Se
  decide al desplegar y se documenta.
- **nft:** se abre 8555/udp+tcp desde 192.168.0.0/24. El tráfico de go2rtc al
  NVR (554) ya está permitido porque es tráfico de la LAN.

## Errores

- Cliente del NVR: timeout de 5 s en control y de 15 s en capturas, y como
  mucho 2 peticiones simultáneas. Los errores se tipifican (`ErrUnauthorized`,
  `ErrUnavailable` y `ErrBadResponse` con el código del NVR).
- UI: cada parcial htmx muestra su propio error sin romper la página. Una
  barra de estado global consulta `/api/health` y avisa si el NVR o go2rtc no
  responden.
- PTZ: el movimiento continuo envía `Param1=1` al pulsar y `Param1=0` al
  soltar. El servidor envía también un stop de seguridad si no recibe nada en
  2 s.
- go2rtc se encarga de reconectar el RTSP, y `video.js` reintenta con espera
  creciente.

## Pruebas

- `nvr`: `httptest` con los XML de `testdata/nvr/`, que salen de `captures/api`
  saneados.
- Vigilantes: los tests unittest de Python se portan uno a uno a tests Go
  basados en tablas, cubriendo los mismos casos.
- `auth`: hash, sesión, caducidad, CSRF y límite de intentos.
- `web`: tests de handlers con `httptest` y un NVR falso, comprobando el
  estado HTTP y los fragmentos HTML clave.
- `media`: test con el resultado esperado de la config de go2rtc.
- Ningún test toca el equipo. Las pruebas reales se registran en
  `docs/test-log.md` con un comando reproducible.
- CI: `go vet`, `go test -race ./...` y la construcción de las imágenes.

## Reparto del trabajo

La implementación usa `/cli-agent-orchestration` según la complejidad de cada
tarea. Los agentes trabajan en un espacio aislado, sin credenciales ni acceso
a la LAN, y solo con fixtures saneados.

- **Tareas acotadas con tests de referencia**, como el port de los
  vigilantes: Codex.
- **Clientes del NVR por área, plantillas y CSS**: agentes secundarios, con
  la interfaz definida de antemano.
- **Esqueleto, `auth`, `media`, integración, despliegue y pruebas contra el
  equipo**: la sesión principal.

## Fuera de alcance

- Grabar en Baphomet o en el NAS.
- Varios usuarios o roles.
- Acceso desde Internet sin VPN.
- ONVIF.
- Notificaciones push en el navegador (ntfy sigue como está).
