# Interfaz web, fases 3 y 4: plan de implementación

> **Para agentes:** SUB-SKILL OBLIGATORIA: usar superpowers:subagent-driven-development
> (recomendado) o superpowers:executing-plans para ejecutar este plan tarea a tarea.
> Los pasos usan casillas (`- [ ]`) para el seguimiento.

**Objetivo:** configurar cámaras y NVR desde la interfaz propia (fase 3),
recibir eventos de movimiento en vivo y consultar qué hay grabado: calendario
mensual y línea de tiempo diaria por cámara (fase 4, primera parte). La
reproducción de grabaciones queda para un plan posterior, porque requiere el
login P6S (Tarea 6, investigación).

**Arquitectura:** el cliente `internal/nvr` gana un archivo por área
(`config.go`, `events.go` y `playback.go`). Las escrituras nunca construyen
XML desde cero: leen el documento actual del NVR, cambian solo los campos
permitidos y lo devuelven entero (`xmlpatch.go`). La web añade las páginas
`/settings` y `/recordings` y un flujo SSE `/api/events`.

**Stack:** Go (biblioteca estándar más `golang.org/x/crypto`;
`github.com/coder/websocket` permitido solo en `events.go`), `html/template`
y htmx.

**Spec:** `docs/superpowers/specs/2026-10-27-web-interface-design.md`
(fases 3 y 4). Plan anterior: `2026-10-27-web-interface-phase1-2.md`.

## Restricciones globales

Siguen vigentes todas las del plan anterior. Además:

- Las escrituras al NVR solo tocan los campos de la lista blanca de cada tipo.
  Todo PUT parte del XML leído en ese momento con GET (lectura, cambio y
  escritura), y nunca envía un documento construido a mano.
- Rangos validados antes de enviar. Si un valor está fuera de rango, se
  devuelve un error de validación y no se hace ninguna petición.
- Nada de formatear, restablecer, borrar grabaciones, cambiar usuarios,
  red, P2P ni firmware. La red del NVR es **solo lectura**.
- La hora de las grabaciones y de `SystemTime` es hora local del NVR. El
  cliente recibe un `*time.Location` (`NVR_TZ`, por defecto
  `America/Santiago`).
- Fixtures en `testdata/nvr/config/`, tal como los devolvió el NVR el
  2026-10-28, ya saneados. Ningún test toca el equipo.
- Los agentes no reciben credenciales ni acceso a la LAN, y trabajan en un
  worktree propio. No hacen commit.

## Mapa de archivos

```
internal/nvr/xmlpatch.go        cambiar hojas de un XML conservando el resto      (Tarea 1)
internal/nvr/config.go          imagen, IR, grabación, hora, disco, red, movimiento, streams (Tarea 1)
internal/nvr/events.go          WebSocket de eventos del NVR                        (Tarea 2)
internal/nvr/playback.go        calendario mensual y segmentos diarios              (Tarea 3)
internal/camera/service.go      métodos nuevos que delegan en nvr                   (Tarea 4)
internal/web/handlers_settings.go, handlers_recordings.go, handlers_events.go     (Tarea 4)
internal/web/templates/settings_camera.html, settings_nvr.html, recordings.html    (Tarea 4)
internal/web/static/js/events.js                                                    (Tarea 4)
```

---

### Tarea 1: configuración en el cliente del NVR (Codex)

**Archivos:** crear `internal/nvr/xmlpatch.go`, `internal/nvr/config.go` y
sus tests `xmlpatch_test.go` y `config_test.go`. No modificar los demás
archivos del paquete, salvo para añadir rutas al NVR falso de los tests.

**Interfaces producidas:**

```go
package nvr

// PatchXML devuelve doc con el texto de cada hoja indicada sustituido. Las
// rutas van separadas por "/" desde el hijo de la raíz (por ejemplo
// "Schedule/TimeBlockList/TimeBlock_0"). Conserva el resto del documento
// byte a byte, incluidos la declaración, los atributos y el espaciado. Si
// una ruta no existe, devuelve error y no modifica nada.
func PatchXML(doc []byte, changes map[string]string) ([]byte, error)

// ValidationError: valor fuera de rango; no se envió nada al NVR.
type ValidationError struct{ Field, Reason string }
func (e *ValidationError) Error() string

type ImageColor struct{ Brightness, Contrast, Saturation, Hue, Sharpness int } // 0..100
func (c *Client) ImageColor(ctx context.Context, ch int) (ImageColor, error)       // GET /Images/{ch}/Color
func (c *Client) SetImageColor(ctx context.Context, ch int, v ImageColor) error    // lectura+PatchXML+PUT

type ImageBasic struct {
	FlipMode    string // close|horizontal|vertical|center (lista de Streams/{ch}/CapabilityV2 si la incluye; si no, estos cuatro)
	WDR         string // close|automatic|open
	NoiseReduce string // close|open|automatic
	BLC         string // close|open
}
func (c *Client) ImageBasic(ctx context.Context, ch int) (ImageBasic, error)
func (c *Client) SetImageBasic(ctx context.Context, ch int, v ImageBasic) error

type IrCut struct {
	Mode        string // intelligent|auto|day|night|time (los valores que admita el XML actual)
	DayStart    string // "HH:MM:SS"
	DayEnd      string // "HH:MM:SS"
	Sensitivity int    // 0..100
}
func (c *Client) IrCut(ctx context.Context, ch int) (IrCut, error)                  // /Images/{ch}/IrCutFilter
func (c *Client) SetIrCut(ctx context.Context, ch int, v IrCut) error

// RecordSchedule: 7 días (0 = domingo, igual que TimeBlock_0) por 48 medias horas.
type RecordSchedule struct {
	StreamType string // main|sub
	AllDay     bool
	Days       [7][48]bool
}
func (c *Client) RecordSchedule(ctx context.Context, ch int) (RecordSchedule, error) // /Record/{ch}/RecordSchedule
func (c *Client) SetRecordSchedule(ctx context.Context, ch int, v RecordSchedule) error

type RecordSettings struct {
	PreRecord   int // segundos, 0..30
	DelayRecord int // segundos, 0..300
	StreamType  string // main|sub
	Overwrite   bool   // RecordWriteCircleType == "writecircle"
}
func (c *Client) RecordSettings(ctx context.Context) (RecordSettings, error)       // /Record
func (c *Client) SetRecordSettings(ctx context.Context, v RecordSettings) error    // RecordMode no se toca

type Clock struct {
	Now        time.Time // SystemTime "20261027T070611", en c.loc
	NTPEnabled bool
	NTPServer  string
	TimeZone   string // texto crudo de NTP/TimeZone
	LastSync   string // texto crudo de LastSuccessSyncNTPTime
}
func (c *Client) Clock(ctx context.Context) (Clock, error)                        // /System/Time + /System/NTP
// SyncNTP pide sincronizar ya (PUT /System/NTP con IsSynchronizeNTPNow=true).
func (c *Client) SyncNTP(ctx context.Context) error

type Disk struct {
	ID              int
	Model           string
	TotalMB, FreeMB int
	State           string // DiskStorageAttribute (writting, idle…)
}
func (c *Client) Disks(ctx context.Context) ([]Disk, error)                        // GET /Disk

type NetworkInfo struct{ IP, Mask, Gateway, DNS1, DNS2, MAC string; DHCP bool }
func (c *Client) Network(ctx context.Context) (NetworkInfo, error)                 // GET /Network/Interfaces (solo lectura)

type Motion struct {
	Enabled     bool
	Sensitivity string // lowest|lower|low|middle|high|higher|highest (campo XML "Senstive")
}
func (c *Client) Motion(ctx context.Context, ch int) (Motion, error)                // /Pictures/{ch}/Motion
func (c *Client) SetMotion(ctx context.Context, ch int, v Motion) error              // solo Enable y Senstive

type StreamInfo struct {
	Codec               string // VideoCodecType
	Width, Height       int
	FPS                 int    // FrameRate/100 (el NVR usa 1000 = 10 fps)
	BitrateKbps         int
	BitrateType         string
}
func (c *Client) Stream(ctx context.Context, ch, stream int) (StreamInfo, error)  // GET /Streams/{ch}/{stream}, 1=MAIN 2=SUB (solo lectura en esta fase)
```

`Client` gana el campo `loc *time.Location` y la opción
`func (c *Client) WithLocation(l *time.Location) *Client`. Por defecto usa
`time.Local`. Este cambio de `client.go` sí se permite.

**Tests** (NVR falso con `httptest` y los fixtures de `testdata/nvr/config/`;
el falso registra los cuerpos de los PUT):
- `PatchXML`: cambia una hoja anidada; conserva byte a byte el resto
  (`bytes.Equal` fuera del texto cambiado); una ruta inexistente da error; en
  una ruta ambigua (hermanos repetidos) también da error; escapa `<&>` en el
  valor.
- Cada GET tipado devuelve los valores del fixture. Por ejemplo, Color
  50/50/50/50/50, `RecordSchedule.AllDay` true con 7×48 `true`, `Clock.Now` =
  2026-10-27 07:06:11 en la zona dada, disco `ST3500418AS` con 476940/464150
  y `Stream(1,1)` HEVC (`H.265`) 2304×1296, 10 fps.
- Cada Set: el PUT es el XML del GET con solo esos campos cambiados (se
  compara con `PatchXML` del fixture). Con un valor fuera de rango (101,
  -1, un modo desconocido o una hora `25:00:00`) devuelve `*ValidationError`
  sin hacer ninguna petición.
- `SetRecordSchedule` convierte `[48]bool` en la cadena de 48 caracteres
  `0/1`.
- `SetRecordSettings` no modifica `RecordMode` ni `RecordDeleteType`.

- [ ] Tests (FAIL) → implementación → `gofmt -l .`, `go vet ./...` y `go test -race ./...` (PASS).

### Tarea 2: eventos en vivo del NVR (Kimi)

**Archivos:** crear `internal/nvr/events.go` y `events_test.go`. Se permite
`go get github.com/coder/websocket@v1.8.14`, o la última v1.8.x disponible.

**Protocolo conocido** (`docs/api-reference.md` §3, `captures/T30-ws-events.txt`):
`ws://host/WebSocket/Connect`; el primer mensaje del cliente es

```json
{"auth":{"user":"<usuario>","mode":"0","time":"<epoch_ms>","pass":"<md5(pass+epoch_ms) en hex>"},
 "req":{"fun":"event","ctrl":"open","ch":"<canal>","stream":"1"}}
```

La respuesta correcta es `{"cmd":"reply","code":0,"msg":"success"}`. Los
mensajes posteriores de eventos **no se han observado todavía** (la
detección de movimiento estaba desactivada), así que el parser debe ser
tolerante.

**Interfaces producidas:**

```go
type Event struct {
	Time    time.Time         // hora de recepción
	Channel int               // si el mensaje lo indica; 0 si no
	Kind    string            // "motion", "videolost", "ai"… o el valor crudo del campo de tipo; "unknown" si no hay
	Active  bool              // inicio (true) o fin (false) si se puede deducir
	Raw     json.RawMessage   // mensaje completo, siempre
}
// Events abre una conexión por canal, autentica, y emite eventos hasta que
// ctx termine. Reconecta con espera creciente (1 s → 30 s) si se cae. Los
// mensajes de respuesta ("cmd":"reply") no se emiten. Un code distinto de 0
// en la respuesta de autenticación devuelve ErrUnauthorized por errc y
// detiene ese canal.
func (c *Client) Events(ctx context.Context, channels []int) (<-chan Event, <-chan error)
```

Heurística de `Kind`, `Channel` y `Active`: buscar claves comunes sin
distinguir mayúsculas (`type`, `event`, `alarm`, `ch`, `channel`, `status`,
`state`, `start`/`stop`) a cualquier nivel del JSON. Documentar la
heurística en un comentario y cubrirla con tests de tabla, con mensajes de
ejemplo inventados y **marcados como hipótesis** en el test.

**Tests:** un servidor WebSocket falso (`httptest` con `websocket.Accept`)
que valida el mensaje de autenticación (md5 correcto para usuario, clave y
`time` dados, `fun` event, `ch` correcto) y responde `success`. Después envía
dos eventos y comprueba que llegan, que no se emite el `reply`, que hay
reconexión tras cerrar el servidor la conexión, y `ErrUnauthorized` con
`code` 1. Todo con `-race`.

### Tarea 3: qué hay grabado (Grok)

**Archivos:** crear `internal/nvr/playback.go` y `playback_test.go`.

**Interfaces producidas:**

```go
// RecordDays devuelve los días (1..31) con grabación en ese mes, en
// cualquier canal. El NVR responde un bitmap de 32 caracteres en
// <RecordInfo>: el carácter i (base 0) es el día i+1. Lo confirma el
// fixture Playback_GetRecordInfoByMonth_20261027.xml (capturado el 27, el
// primer día de grabación: solo marca el 27). El de 2026-10-28 marca 26, 27
// y 28; el 26 probablemente sale del cambio de zona horaria del NVR (hecho
// el 27). No hay que corregirlo: se muestra lo que diga el NVR.
func (c *Client) RecordDays(ctx context.Context, year int, month time.Month) ([]int, error)
// GET /Playback/GetRecordInfoByMonth?Year=YYYY&Month=M

type Segment struct {
	Start, End time.Time // en c.loc (hora local del NVR)
	Type       string    // general|manual|alarm|motion|ai (texto crudo)
}
// RecordSegments devuelve los tramos grabados de un canal (1..N) en un día,
// ordenados por Start. Une tramos contiguos del mismo tipo si el hueco es
// de 2 s o menos.
func (c *Client) RecordSegments(ctx context.Context, ch int, day time.Time) ([]Segment, error)
// GET /Playback/GetRecordInfoByDay?Channel={ch}&RecordType=255&Year=&Month=&Day= (mes y día sin cero inicial)
```

Atención: en la respuesta, `<Channel>` es base 0 (`0` para el canal 1), pero
el parámetro de la petición es base 1 (verificado: `Channel=1` devuelve
`<Channel>0`). Si no hay grabaciones, el NVR devuelve una lista vacía o un
`ResponseStatus`: ambos casos dan una lista vacía sin error, salvo que
`statusCode` sea `-1` u otro código de error, en cuyo caso se devuelve
`BadResponseError`.

**Tests:** con los fixtures `Playback_GetRecordInfoByMonth_20261027.xml` (→ [27]),
`Playback_GetRecordInfoByMonth.xml` (→ [26 27 28]) y
`Playback_GetRecordInfoByDay.xml` (9 tramos del canal 1 el día 28). Comprobar los días; los
segmentos ordenados y en la zona `America/Santiago`; la unión de tramos con
huecos de 2 s o menos (fixture sintético); la consulta exacta enviada
(`Channel=1`, `Month=10`, `Day=28`, sin ceros) y la lista vacía.

### Tarea 4: páginas de configuración, grabaciones y eventos (sesión principal)

Depende de las Tareas 1–3. Se hace al integrarlas.

- `/settings/camera/{ch}`: formularios htmx por bloque (imagen, IR,
  movimiento y horario de grabación), cada uno con `hx-put` y un parcial
  `_result`. Las escrituras exigen CSRF. Los streams se muestran en solo
  lectura.
- `/settings/nvr`: hora (con el botón "Sincronizar NTP"), disco (capacidad
  libre y estado), red (solo lectura) y ajustes de grabación.
- `/recordings`: calendario del mes (con los días con grabación marcados y
  navegación entre meses) y, para el día y la cámara elegidos, una línea de
  tiempo SVG de 24 h con los tramos coloreados por tipo. Al pasar el cursor
  se ve la hora de inicio y fin. La reproducción muestra el aviso
  "Reproducción pendiente (requiere el protocolo P6S)".
- `/api/events`: Server-Sent Events. `camera.Service` mantiene **una**
  suscripción `nvr.Events` compartida y un repetidor a N navegadores. La UI
  muestra un aviso breve ("Movimiento en cámara 1") y un punto en la celda
  de la cuadrícula. Los eventos `unknown` se registran en el log con `Raw`,
  para aprender el formato.
- Navegación: añadir "Grabaciones" y "Ajustes" en la cabecera.
- Tests de handlers con un `CameraService` falso, igual que en las fases
  anteriores.

### Tarea 5: verificación con el equipo y despliegue (sesión principal más el propietario)

- Leer con la interfaz todas las secciones y comprobar que coinciden con la
  interfaz web del NVR.
- Hacer escrituras reversibles: brillo 50 → 55 → 50, sensibilidad IR,
  activar la detección de movimiento en el canal 1 y desactivarla, y SyncNTP.
  Cada una se verifica leyendo de vuelta.
- Activar temporalmente el movimiento en el canal 1 para capturar mensajes
  reales de eventos, documentar su formato en `docs/api-reference.md` y
  ajustar la heurística de la Tarea 2.
- Registrar los resultados en `docs/test-log.md`, publicar y desplegar.

### Tarea 6: investigación del login P6S (agente de análisis más la sesión principal)

Objetivo: la fórmula de `UserLogin/LoginFlag`, que desbloquea la
reproducción de grabaciones y el audio bidireccional.

- El propietario copia la instalación de EasyVideo de la VM Windows a
  `captures/easyvideo/`, que está ignorado por git.
- Un agente, en un directorio aislado **sin credenciales ni red**, analiza
  estáticamente los binarios: busca las cadenas `LoginGetFlag`, `UserLogin`
  y `LoginFlag`, sigue la construcción del campo e identifica el algoritmo
  (MD5, orden de concatenación y formato del reto). Entrega la fórmula en
  pseudocódigo y las direcciones o funciones donde la encontró.
- La sesión principal verifica la fórmula en local contra la pareja
  capturada (`LoginFlag="862446998"` → `63d45a39…`, usuario `admin`), con la
  contraseña del archivo de entorno y **sin enviar nada al equipo**. Solo
  si coincide, se prueba un login real.
- Con el login resuelto, se escribe un plan propio para la reproducción
  (`RecordPlay…` por P6S, que hay que observar en una captura de EasyVideo
  reproduciendo) y para el audio bidireccional.

## Reparto

| Tarea | Ejecutor | Motivo |
|---|---|---|
| 1 | Codex (worktree `p3-config`) | la más grande, mecánica, con fixtures y reglas precisas |
| 2 | Kimi (worktree `p3-events`) | acotada; protocolo pequeño pero con incertidumbre |
| 3 | Grok (worktree `p4-playback`) | pequeña y bien definida |
| 4, 5 | sesión principal | integración, seguridad y pruebas con el equipo |
| 6 | Claude (directorio aislado) cuando lleguen los binarios | análisis estático largo, sin acceso a nada sensible |
