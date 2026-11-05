# Diseño: eventos de visión por MQTT con Home Assistant Discovery

Fecha: 2026-11-04. Estado: aprobado (secciones 1–2 en chat; 3–4 pendientes de revisión).
Continúa el subproyecto «MQTT para Home Assistant» de
`2026-11-03-person-detection-design.md`.

## Objetivo

Publicar en MQTT la presencia de personas, el estado de las cámaras y los eventos de
visión, de modo que Home Assistant cree las entidades solo (MQTT Discovery) sin
configuración manual más allá de añadir la integración MQTT una vez.

Fuera de alcance: streaming de vídeo en HA (go2rtc ya es accesible por loopback en
`127.0.0.1:8554`; la entidad `camera` de MQTT no admite streams), QoS 1/2, MQTT 5.

## Decisiones

| Tema | Decisión |
|---|---|
| Cliente | Propio, MQTT 3.1.1, mínimo y endurecido; paho como referencia de codificación. |
| QoS | Solo 0. Al reconectar se republica todo el estado retenido. |
| Presencia | Por cámara (agregada) y por zona. |
| Captura | No se envía el JPEG: se publica una **URL firmada** en `url_topic`; HA la descarga. |
| HA status | Suscripción a `homeassistant/status`; con `online` se republica discovery y estado. |
| Orden | El `Dispatcher` entrega a cada sink en orden mediante cola y goroutine propias. |

## 1. Arquitectura y broker

```
detector ─► engine ─► Bus ─► Dispatcher ─┬─► History (síncrono, guarda el JPEG)
                                         ├─► cola ntfy ─► NtfySink   (1 goroutine)
                                         └─► cola MQTT ─► MQTTSink   (1 goroutine)
                                                               │
                                         internal/mqtt.Client ◄┘
                                                  │ TCP 127.0.0.1:1883
                                              mosquitto ◄── Home Assistant (red host)
                                                               │
                         GET /vision/snapshots/...?sig= ◄──────┘ (url_topic)
```

### Dispatcher ordenado

`events.Dispatcher` crea por sink una cola `chan vision.Event` de capacidad 64 y una
goroutine que llama a `Send` en orden, con el timeout de 10 s actual. Si la cola está
llena, el evento se descarta para ese sink y se registra con `logx.Printf`. `History.Record`
y `OnEvent` siguen síncronos y antes de encolar. Las goroutines terminan con el contexto.

### Mosquitto (`deploy/baphomet/compose.yaml`)

- Imagen `eclipse-mosquitto:2.0.<x>` con tag exacto (verificar el último 2.0.x estable).
- `network_mode: host`; `mosquitto.conf` con `listener 1883 127.0.0.1`,
  `allow_anonymous false`, `password_file`, `acl_file`, `persistence true`,
  `persistence_location /mosquitto/data/`, log a stdout.
- Secretos:
  - `mqtt_passwd`: fichero de hashes generado por el propietario con `mosquitto_passwd`
    (usuarios `opennvr` y `homeassistant`); montado como secreto.
  - `mqtt_pass`: contraseña en claro del usuario `opennvr` para `opennvr-web`
    (`MQTT_PASS_FILE=/run/secrets/mqtt_pass`, leído con `config.Secret("MQTT_PASS")`).
- ACL (fichero en el repo, `deploy/baphomet/mosquitto/acl`):
  - `user opennvr`: `topic readwrite opennvr/#`, `topic write homeassistant/+/+/+/config`,
    `topic read homeassistant/status`.
  - `user homeassistant`: `topic read #`, `topic write homeassistant/status`.
- Volumen con nombre para `/mosquitto/data`; `cap_drop: ALL`, `no-new-privileges`,
  healthcheck sin paréntesis ni comillas complejas (p. ej. `mosquitto_sub` con
  `-t '$SYS/broker/uptime' -C 1 -W 3` y credenciales de un fichero, o comprobación de
  puerto; elegir lo que funcione con podman-compose 1.0.6).
- `.env.example` y `deploy/baphomet/README.md`: variables nuevas, comando para generar
  `mqtt_passwd`, paso manual de HA (Ajustes → Dispositivos → Añadir integración → MQTT,
  broker `127.0.0.1`, puerto 1883, usuario `homeassistant`).

## 2. Cliente `internal/mqtt`

MQTT 3.1.1 sobre TCP (`mqtt://`) o TLS (`mqtts://`, verificación por defecto).

```go
type Message struct { Topic string; Payload []byte; Retain bool }

type Client struct {
    URL       string        // mqtt://host:port o mqtts://host:port; sin userinfo
    ClientID  string
    User, Pass string
    Will      *Message      // LWT
    KeepAlive time.Duration // por defecto 30 s
    OnConnect func()        // tras CONNACK aceptado y SUBSCRIBE enviados
    Queue     int           // capacidad de la cola de salida, por defecto 256
}

func (c *Client) Run(ctx context.Context) error                 // bucle de conexión
func (c *Client) Publish(topic string, payload []byte, retain bool) error // no bloquea
func (c *Client) Subscribe(filter string, h func(Message))      // antes de Run
```

- `Run`: conecta, envía CONNECT (clean session, usuario/contraseña, will), espera
  CONNACK (código 0 o error tipado), envía SUBSCRIBE de los filtros registrados, llama a
  `OnConnect`, y atiende lectura, escritura y keepalive hasta error o fin de contexto.
  Reintenta con backoff exponencial 1 s → 60 s con jitter. Con contexto cancelado envía
  DISCONNECT y devuelve `nil`. Un CONNACK con «bad username or password» o «not
  authorized» sigue reintentando con el backoff máximo y lo registra una vez por cambio.
- `Publish`: valida el topic y encola; si la cola está llena devuelve `ErrQueueFull`
  (el llamante registra). Sin conexión, los mensajes encolados se envían al reconectar
  hasta llenar la cola (el estado completo se republica en `OnConnect` de todos modos).
- Seguridad y robustez:
  - Paquete entrante máximo 64 KiB; remaining length > 4 bytes o mal formada → cierre.
  - Deadlines de escritura (10 s) y lectura (1,5 × keepalive); PINGREQ cada keepalive;
    sin PINGRESP a tiempo → reconexión.
  - Topics: UTF-8 válido, sin U+0000, longitud 1–65535 bytes, sin `+`/`#` al publicar.
  - Solo se procesan del servidor: CONNACK, SUBACK, PINGRESP, PUBLISH QoS 0. QoS > 0
    entrante o paquetes inesperados → cierre de la conexión.
  - Contraseña y userinfo nunca aparecen en logs ni errores; `URL` con userinfo se
    rechaza.
- Tests con un broker falso en proceso (`net.Listener`) que decodifica paquetes.

## 3. `MQTTSink` (`internal/vision/events/mqtt.go`)

```go
type Publisher interface {
    Publish(topic string, payload []byte, retain bool) error
}

type CameraInfo struct { Cam int; Name string; Online *bool } // nil = desconocido

type MQTTSink struct {
    Pub         Publisher
    Prefix      string                         // "opennvr"
    Discovery   string                         // "homeassistant"
    Cameras     func() []CameraInfo
    Zones       func(cam int) []vision.Zone
    SnapshotURL func(rel string) string        // rel = Event.Snapshot; "" si no hay firma
}

func (s *MQTTSink) Send(ctx context.Context, ev vision.Event) error
func (s *MQTTSink) Resync()            // discovery + estado completo; idempotente
func (s *MQTTSink) HAStatus(payload []byte) // "online" → Resync
```

El sink no importa `internal/mqtt`: la app adapta `func(mqtt.Message)` a `HAStatus(m.Payload)`.

### Estado interno (protegido por mutex)

Por cámara: conjunto de zonas ocupadas, `tracking bool`, `connected *bool`, última URL
de captura, y el conjunto de object_ids de zona publicados en discovery.

- `zone_enter`: añade `ZoneID` (vacío cuenta para «persona» pero no tiene sensor de zona).
- `zone_leave`: quita `ZoneID`.
- `tracking_start` / `tracking_stop`: `tracking = true/false`.
- `persona = len(zonas) > 0 || tracking`.
- `camera_offline`: `connected = OFF` y además vacía zonas y tracking (evita `ON` colgado).
- `camera_online`: `connected = ON`.
- Si `ev.Snapshot != ""` y `SnapshotURL` devuelve URL: se publica en `snapshot_url`.
- Solo se publican los topics cuyo valor cambia, salvo en `Resync`, que publica todo.

### Topics (prefijo `opennvr`)

| Topic | Payload | Retain |
|---|---|---|
| `opennvr/status` | `online` / `offline` (LWT y DISCONNECT limpio publica `offline`) | sí |
| `opennvr/cam<N>/person` | `ON` / `OFF` | sí |
| `opennvr/cam<N>/zone/<zid>` | `ON` / `OFF` | sí |
| `opennvr/cam<N>/connected` | `ON` / `OFF` (no se publica si se desconoce) | sí |
| `opennvr/cam<N>/snapshot_url` | URL firmada | sí |
| `opennvr/cam<N>/event` | JSON del evento | no |
| `opennvr/event` | JSON de eventos sin cámara (`detector_down`) | no |

JSON de evento (compatible con la entidad `event` de HA):
`{"event_type":"zone_enter","id":"…","zone_id":"…","zone_name":"…","track_id":3,"at":"RFC3339","message":"…","snapshot_url":"…"}`
(campos vacíos omitidos).

`<zid>`: `Zone.ID` en minúsculas con todo lo que no sea `[a-z0-9_-]` sustituido por `_`;
si dos zonas colisionan, se añade `_` + 6 hex del SHA-256 del ID original.

### Discovery (`homeassistant/<component>/<node>/<object>/config`, retenido)

Dispositivo hub: `{"identifiers":["opennvr"],"name":"open-nvr","manufacturer":"open-nvr"}`.
Dispositivo por cámara: `identifiers ["opennvr_cam<N>"]`, `name` = nombre de la cámara o
`Cámara <N>`, `via_device: "opennvr"`. Todas las entidades llevan
`availability_topic: opennvr/status`, `unique_id` estable, `origin` con nombre `open-nvr`.

| Entidad | component / node / object | Claves |
|---|---|---|
| Persona | `binary_sensor/opennvr_cam<N>/person` | `state_topic`, `device_class: occupancy` |
| Zona | `binary_sensor/opennvr_cam<N>/zone_<zid>` | `state_topic`, `occupancy`, `name` = `Zone.Name` o ID |
| Conectada | `binary_sensor/opennvr_cam<N>/connected` | `connectivity`, `entity_category: diagnostic` |
| Captura | `image/opennvr_cam<N>/snapshot` | `url_topic` |
| Eventos cámara | `event/opennvr_cam<N>/events` | `event_types` = los 7 tipos de cámara |
| Eventos sistema | `event/opennvr/events` | `event_types: ["detector_down"]` |

`Resync` publica: `opennvr/status online`, todas las configuraciones, el estado actual.
Las zonas que estaban publicadas y ya no existen reciben payload vacío retenido en su
config y en su topic de estado (HA borra la entidad). La app llama a `Resync` tras
`SetConfig`. `OnConnect` del cliente llama a `Resync`.

## 4. URL firmada, configuración, errores y tests

### URL firmada de capturas

- Paquete pequeño `internal/vision/snapsign` (o funciones en `events`):
  `Signer{Key []byte}`, `Sign(rel string) string` (hex de HMAC-SHA256 truncado a 32 bytes
  → 64 hex) y `Verify(rel, sig string) bool` con `hmac.Equal`.
- Clave: 32 bytes aleatorios en `<VISION_DATA>/snapshot-url.key` (0600), creada al primer
  arranque si no existe. Rotación = borrar el fichero y reiniciar.
- URL: `<MQTT_SNAPSHOT_BASE>/vision/snapshots/<rel sin "snapshots/">?sig=<hex>`.
  `MQTT_SNAPSHOT_BASE` por defecto `http://` + `WEB_LISTEN`.
- Web: `GET /vision/snapshots/{path...}` acepta la petición sin sesión **solo** si
  `sig` es válida para esa ruta; si no, se mantiene el flujo autenticado actual. Mismas
  validaciones de ruta (`SnapshotPath`). Con firma: `Cache-Control: private, max-age=86400`.
  Firma inválida sin sesión → mismo comportamiento que hoy sin sesión.

### Configuración (`cmd/opennvr/cmd_web.go`, `visionapp.Config`)

| Variable | Defecto | Uso |
|---|---|---|
| `VISION_MQTT` | `false` | Activa el sink (requiere visión activa). |
| `MQTT_URL` | `mqtt://127.0.0.1:1883` | Broker. |
| `MQTT_USER` | `opennvr` | Usuario. |
| `MQTT_PASS` / `MQTT_PASS_FILE` | — | `config.Secret`. Vacía con `VISION_MQTT=true` → error de arranque claro. |
| `MQTT_CLIENT_ID` | `opennvr-web` | Client ID. |
| `MQTT_SNAPSHOT_BASE` | `http://$WEB_LISTEN` | Base de las URL firmadas. |

`cmd_web.go` crea el `mqtt.Client` (con will `opennvr/status offline` retenido), lo pasa
a `visionapp.Config`; la app construye el `MQTTSink`, registra
`Subscribe("homeassistant/status", func(m mqtt.Message) { sink.HAStatus(m.Payload) })`, `OnConnect = sink.Resync`, y lanza
`client.Run` con el contexto de la app. Al apagar: publica `offline` y DISCONNECT.

### Errores

- Broker caído: el cliente reintenta; el sink sigue actualizando su estado y la
  reconexión republica todo. Nada bloquea al dispatcher ni a la web.
- Cola llena (dispatcher o cliente): descarte + log con limitación (máx. 1 por minuto).
- Fallo al crear la clave de firma: el sink publica sin `snapshot_url` y lo registra.

### Tests (TDD)

- `internal/mqtt`: codificación/decodificación de paquetes (vectores de paho), CONNECT con
  will y credenciales, CONNACK rechazado, PUBLISH retain, SUBSCRIBE y entrega a handler,
  keepalive y reconexión ante silencio, límites (64 KiB, remaining length, QoS > 0
  entrante), validación de topics, cancelación limpia con DISCONNECT, que la contraseña
  no aparezca en errores.
- `events`: dispatcher ordenado (1000 eventos en orden, sink lento no bloquea a otro,
  descarte con cola llena); `MQTTSink` con publisher falso: mapeo evento → topics,
  agregado de persona, `camera_offline` resetea, discovery JSON (golden), borrado de zonas,
  `HAStatus("online")` → `Resync`, sanitizado y colisión de `zid`.
- `snapsign` y web: firma válida sin sesión → 200; inválida/ausente sin sesión → igual
  que hoy; path traversal con firma de otra ruta → 404.
- Integración opcional local: `mosquitto` en contenedor rootless + `mosquitto_sub`.
- `go test ./...` y `go vet ./...` en verde.
