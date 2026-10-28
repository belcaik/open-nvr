# Interfaz web propia, fases 1 y 2: plan de implementación

> **Para agentes:** SUB-SKILL OBLIGATORIA: usar superpowers:subagent-driven-development
> (recomendado) o superpowers:executing-plans para ejecutar este plan tarea a tarea.
> Los pasos usan casillas (`- [ ]`) para el seguimiento.

**Objetivo:** reescribir en Go los vigilantes `nvr-watch` y `egress-watch`
como un binario único `opennvr`, y añadir `opennvr web`: login, directo de
las 4 cámaras con audio y PTZ con stop de seguridad, servido en
`https://nvr.belcaik.com`.

**Arquitectura:** un módulo Go con paquetes en `internal/` y un único binario
con subcomandos. go2rtc reparte el vídeo y la app web es la única puerta de
entrada: valida la sesión y reenvía la señalización a go2rtc por loopback.
Caddy termina TLS con un certificado DNS-01 de Cloudflare.

**Stack:** Go (biblioteca estándar más `golang.org/x/crypto`), `html/template`,
htmx 2.0.4 embebido, `video-rtc.js` de go2rtc embebido, go2rtc 1.9.x
(variante `-hardware`), Caddy con `caddy-dns/cloudflare`, podman-compose 1.0.6.

**Spec:** `docs/superpowers/specs/2026-10-27-web-interface-design.md`

## Restricciones globales

- Módulo `github.com/belcaik/open-nvr`, `go 1.24` en `go.mod` como mínimo. El
  binario se compila con `CGO_ENABLED=0`.
- Dependencias externas permitidas en las fases 1 y 2: solo
  `golang.org/x/crypto`. `github.com/coder/websocket` se aplaza a la fase 3:
  el proxy WebSocket usa `httputil.ReverseProxy`.
- El sentido de las dependencias es `web → camera → nvr | media`. `nvr` y
  `media` no importan `web`.
- Las credenciales salen solo de variables de entorno o de archivos
  `*_FILE`. Toda salida de log pasa por `config.Redact`. Ningún test, fixture
  ni log contiene la contraseña, el número de serie, el UID P2P, el ChipID ni
  las MAC reales.
- Ningún test toca el NVR ni la red: se usan `httptest` y fixtures de
  `testdata/`.
- Los agentes delegados no reciben credenciales, acceso a la LAN ni SSH a
  Baphomet.
- Commits atómicos con Conventional Commits, comentarios en español e
  identificadores en inglés, como el código existente.
- Formato de los archivos de estado: los JSON que ya existen en los volúmenes
  de Baphomet (`state.json`, `baseline.json`, `last_snapshot.json`) deben
  leerse sin migración.
- Las operaciones destructivas del NVR (formatear, restablecer, borrar
  grabaciones, telnet, firmware, usuarios) no se implementan. Tampoco se
  ofrece `Presets/Remove` sin confirmación en la UI.
- Imagen base de producción: `docker.io/library/busybox:1.36.1-musl` (permite
  healthchecks `CMD-SHELL`, la única forma que podman-compose 1.0.6 no
  estropea). Esto sustituye a la opción distroless de la spec, y la Tarea 5
  actualiza la spec.

## Mapa de archivos

```
go.mod, go.sum
cmd/opennvr/main.go                 enrutado de subcomandos
internal/config/{env,secret}.go     entorno, secretos *_FILE, redacción
internal/logx/log.go                log a stdout con fecha y redactado
internal/statefile/statefile.go     JSON atómico
internal/notify/{throttle,ntfy}.go  dedup/límite y cliente ntfy
internal/health/health.go           latido en archivo y comprobación
internal/watch/nvrwatch/*.go        port de services/nvr-watch
internal/watch/egresswatch/*.go     port de services/egress-watch
internal/nvr/{client,device,ptz,snapshot}.go
internal/camera/{service,ptzguard}.go
internal/auth/{password,session,csrf,ratelimit}.go
internal/media/{go2rtc,proxy}.go
internal/web/server.go, middleware.go
internal/web/handlers/{login,live,camera,ptz,health}.go
internal/web/templates/*.html
internal/web/static/{app.css,js/video.js,js/ptz.js,vendor/htmx.min.js,vendor/video-rtc.js,vendor/LICENSE-go2rtc}
testdata/nvr/*.xml                  XML saneados
Dockerfile                          imagen única
deploy/baphomet/compose.yaml        servicios con la imagen única + web + go2rtc
deploy/baphomet/caddy/{Dockerfile,nvr.Caddyfile}
deploy/baphomet/nftables/opennvr.nft   puertos 8095 y 8555
.github/workflows/images.yml        Go en lugar de Python
```

Se borran: `services/` entero, `services/*/Dockerfile` y las entradas
nvr-watch/egress-watch de `compose.build.yaml`.

---

## Fase 1: base Go y migración de los vigilantes

### Tarea 1: esqueleto del módulo, config, log y estado

**Archivos:**
- Crear: `go.mod`, `cmd/opennvr/main.go`, `internal/config/env.go`,
  `internal/config/secret.go`, `internal/logx/log.go`,
  `internal/statefile/statefile.go`
- Test: `internal/config/env_test.go`, `internal/config/secret_test.go`,
  `internal/statefile/statefile_test.go`

**Interfaces:**
- Produce:
  - `config.Str(name, def string) string`, `config.Int(name string, def int) int`,
    `config.Float(name string, def float64) float64`,
    `config.Bool(name string, def bool) bool`,
    `config.List(name string, def []string) []string`,
    `config.Ports(name string, def []int) []int` (ordenados y sin repetir)
  - `config.Secret(name string) string`: lee `NAME_FILE` si existe el
    archivo y, si no, `NAME`. Registra el valor para redacción.
  - `config.Register(v string)`, `config.RegisterBasic(user, pass string)`,
    `config.Redact(s string) string`, `config.ResetSecrets()` (solo tests)
  - `logx.Printf(format string, args ...any)`: escribe
    `2006-01-02T15:04:05-0700 <mensaje redactado>` en stdout.
  - `statefile.Load(path string, v any) (found bool, err error)`,
    `statefile.Save(path string, v any) error` (archivo `.tmp` más rename,
    sangría de 1 espacio y `\n` final)
  - `cmd/opennvr`: subcomandos `version`, `nvr-watch`, `egress-watch`,
    `healthcheck` y `web` (los tres últimos se registran en sus tareas). La
    variable `version` se fija con `-ldflags "-X main.version=..."`.

Equivalencias con Python: `services/common/opennvr_common/{config,creds,log,statefile}.py`.
Hay que conservar el comportamiento exacto: `Bool` acepta
`1,true,yes,si,sí,on`, el valor se compara con `strings.TrimSpace` y
`strings.ToLower`, y no se registran secretos de menos de 3 caracteres. Un
archivo vacío equivale a que no haya secreto. La redacción reemplaza primero
los secretos más largos.

- [ ] **Paso 1: crear el módulo**

```bash
cd /home/belcaik/Dev/open-nvr
go mod init github.com/belcaik/open-nvr
go mod edit -go=1.24
```

- [ ] **Paso 2: escribir los tests que fallan**

`internal/config/env_test.go`:

```go
package config

import (
	"reflect"
	"testing"
)

func TestEnvHelpers(t *testing.T) {
	t.Setenv("X_STR", "  hola ")
	t.Setenv("X_INT", "42")
	t.Setenv("X_BOOL", "Sí")
	t.Setenv("X_LIST", " a, b ,,c ")
	t.Setenv("X_PORTS", "554,80,554")
	if got := Str("X_STR", "d"); got != "hola" {
		t.Fatalf("Str = %q", got)
	}
	if got := Str("X_MISSING", "d"); got != "d" {
		t.Fatalf("Str default = %q", got)
	}
	if got := Int("X_INT", 1); got != 42 {
		t.Fatalf("Int = %d", got)
	}
	if !Bool("X_BOOL", false) || Bool("X_MISSING", false) {
		t.Fatal("Bool")
	}
	if got := List("X_LIST", nil); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("List = %v", got)
	}
	if got := Ports("X_PORTS", nil); !reflect.DeepEqual(got, []int{80, 554}) {
		t.Fatalf("Ports = %v", got)
	}
	if got := Ports("X_MISSING", []int{9000, 80}); !reflect.DeepEqual(got, []int{80, 9000}) {
		t.Fatalf("Ports default = %v", got)
	}
}
```

`internal/config/secret_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecretFileWinsAndIsRedacted(t *testing.T) {
	ResetSecrets()
	p := filepath.Join(t.TempDir(), "s")
	os.WriteFile(p, []byte("archivo-secreto\n"), 0o600)
	t.Setenv("S_FILE", p)
	t.Setenv("S", "entorno-secreto")
	if got := Secret("S"); got != "archivo-secreto" {
		t.Fatalf("Secret = %q", got)
	}
	if got := Redact("x archivo-secreto y"); got != "x *** y" {
		t.Fatalf("Redact = %q", got)
	}
}

func TestSecretEnvFallbackAndShortIgnored(t *testing.T) {
	ResetSecrets()
	t.Setenv("S", "ab")
	if Secret("S") != "ab" || Redact("ab") != "ab" {
		t.Fatal("los secretos de menos de 3 caracteres no se redactan")
	}
}

func TestRegisterBasic(t *testing.T) {
	ResetSecrets()
	RegisterBasic("admin", "clave123")
	if got := Redact("Authorization: Basic YWRtaW46Y2xhdmUxMjM="); got != "Authorization: Basic ***" {
		t.Fatalf("Redact = %q", got)
	}
}
```

`internal/statefile/statefile_test.go`:

```go
package statefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	var v map[string]any
	found, err := Load(p, &v)
	if found || err != nil {
		t.Fatalf("Load inexistente = %v, %v", found, err)
	}
	if err := Save(p, map[string]any{"b": 1, "a": "é"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != "{\n \"a\": \"é\",\n \"b\": 1\n}\n" {
		t.Fatalf("formato = %q", raw)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("quedó el .tmp")
	}
	found, err = Load(p, &v)
	if !found || err != nil || v["a"] != "é" {
		t.Fatalf("Load = %v %v %v", found, err, v)
	}
}
```

- [ ] **Paso 3: comprobar que fallan.** Ejecutar `go test ./internal/...`.
  Resultado esperado: FAIL por funciones sin definir.

- [ ] **Paso 4: implementar**

`internal/config/env.go`:

```go
// Package config lee la configuración del entorno y los secretos de archivo.
package config

import (
	"os"
	"sort"
	"strconv"
	"strings"
)

func Str(name, def string) string {
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func Int(name string, def int) int {
	if v := Str(name, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func Float(name string, def float64) float64 {
	if v := Str(name, ""); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func Bool(name string, def bool) bool {
	switch strings.ToLower(Str(name, "")) {
	case "":
		return def
	case "1", "true", "yes", "si", "sí", "on":
		return true
	}
	return false
}

func List(name string, def []string) []string {
	v := Str(name, "")
	if v == "" {
		return append([]string(nil), def...)
	}
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func Ports(name string, def []int) []int {
	defs := make([]string, len(def))
	for i, p := range def {
		defs[i] = strconv.Itoa(p)
	}
	seen := map[int]bool{}
	var out []int
	for _, s := range List(name, defs) {
		if p, err := strconv.Atoi(s); err == nil && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}
```

`internal/config/secret.go`:

```go
package config

import (
	"encoding/base64"
	"os"
	"sort"
	"strings"
	"sync"
)

var (
	mu      sync.RWMutex
	secrets = map[string]bool{}
)

// Register añade un valor que nunca debe salir en claro.
func Register(v string) {
	if len(v) < 3 {
		return
	}
	mu.Lock()
	secrets[v] = true
	mu.Unlock()
}

// RegisterBasic registra también la cabecera Basic que se construye con ellos.
func RegisterBasic(user, pass string) {
	if pass != "" {
		Register(base64.StdEncoding.EncodeToString([]byte(user + ":" + pass)))
	}
}

// Secret devuelve el contenido de NAME_FILE (si el archivo existe) o NAME.
func Secret(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if p := os.Getenv(name + "_FILE"); p != "" {
		// Como en Python: el archivo manda si existe; si no, se usa NAME.
		if b, err := os.ReadFile(p); err == nil {
			v = strings.TrimSpace(string(b))
		}
	}
	Register(v)
	return v
}

// Redact sustituye cualquier secreto registrado por ***.
func Redact(s string) string {
	mu.RLock()
	list := make([]string, 0, len(secrets))
	for k := range secrets {
		list = append(list, k)
	}
	mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, k := range list {
		s = strings.ReplaceAll(s, k, "***")
	}
	return s
}

// ResetSecrets solo para pruebas.
func ResetSecrets() {
	mu.Lock()
	secrets = map[string]bool{}
	mu.Unlock()
}
```

Test adicional en `secret_test.go`:

```go
func TestSecretMissingFileFallsBackToEnv(t *testing.T) {
	ResetSecrets()
	t.Setenv("S_FILE", filepath.Join(t.TempDir(), "no-existe"))
	t.Setenv("S", "entorno-secreto")
	if got := Secret("S"); got != "entorno-secreto" {
		t.Fatalf("Secret = %q", got)
	}
}
```

`internal/logx/log.go`:

```go
// Package logx escribe en stdout (lo recoge podman/journald), siempre redactado.
package logx

import (
	"fmt"
	"os"
	"time"

	"github.com/belcaik/open-nvr/internal/config"
)

func Printf(format string, args ...any) {
	msg := config.Redact(fmt.Sprintf(format, args...))
	fmt.Fprintf(os.Stdout, "%s %s\n", time.Now().Format("2006-01-02T15:04:05-0700"), msg)
}
```

`internal/statefile/statefile.go`:

```go
// Package statefile guarda estado en JSON con escritura atómica.
package statefile

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

func Load(path string, v any) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

func Save(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil { // Encode añade el \n final
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
```

(Añadir `"bytes"` a los imports.)

`cmd/opennvr/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

var version = "dev"

// commands lo rellenan los archivos cmd_*.go de cada subcomando.
var commands = map[string]func(args []string) int{
	"version": func([]string) int { fmt.Println(version); return 0 },
}

func main() {
	if len(os.Args) < 2 || commands[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "uso: opennvr <web|nvr-watch|egress-watch|healthcheck|hash-password|version> [args]")
		os.Exit(2)
	}
	os.Exit(commands[os.Args[1]](os.Args[2:]))
}
```

- [ ] **Paso 5: comprobar que pasan.** Ejecutar `go vet ./... && go test ./...`.
  Resultado esperado: PASS.
- [ ] **Paso 6: añadir a `.gitignore`** el binario `/opennvr` y hacer commit.

```bash
git add go.mod cmd internal .gitignore
git commit -m "feat(go): add module skeleton with config, logging and state files"
```

### Tarea 2: notificaciones (throttle y ntfy) y latido de salud

**Archivos:**
- Crear: `internal/notify/throttle.go`, `internal/notify/ntfy.go`,
  `internal/health/health.go`, `cmd/opennvr/cmd_healthcheck.go`
- Test: `internal/notify/throttle_test.go`, `internal/notify/ntfy_test.go`,
  `internal/health/health_test.go`

**Interfaces:**
- Consume: `config.Redact`, `logx.Printf` (Tarea 1)
- Produce:

```go
package notify

const (PriorityLow = 2; PriorityDefault = 3; PriorityHigh = 4; PriorityUrgent = 5)

// ThrottleState se serializa igual que el dict de Python:
// {"last": {clave: epoch}, "sent": [epoch...], "suppressed": n}
type ThrottleState struct {
	Last       map[string]float64 `json:"last"`
	Sent       []float64          `json:"sent"`
	Suppressed int                `json:"suppressed"`
}
type Throttle struct {
	State       *ThrottleState
	MinInterval float64
	MaxPerHour  int
}
func NewThrottle(st *ThrottleState, minInterval float64, maxPerHour int) *Throttle
// interval < 0 usa MinInterval.
func (t *Throttle) Allow(key string, now, interval float64) bool
func (t *Throttle) Forget(key string, now float64)
func (t *Throttle) TakeSuppressed() int
func (t *Throttle) Prune(now, maxAge float64) // maxAge por defecto 7*86400 en los llamadores

type Payload struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags"`
}
type Sender func(url string, p Payload, token string) error
func HTTPSender(url string, p Payload, token string) error // POST url+"/" JSON, Bearer, timeout 10 s, error si el código no es 2xx
type Notifier struct {
	URL, Topic, Token, Prefix string
	Throttle *Throttle
	Send     Sender // nil = HTTPSender
}
// Send equivale a Notifier.send de Python; interval < 0 = por defecto.
func (n *Notifier) Notify(key, title, message string, priority int, tags []string, interval, now float64) bool
```

```go
package health
// Beat escribe la hora actual en path (por ejemplo /tmp/heartbeat).
func Beat(path string) error
// Check devuelve nil si path se modificó hace menos de maxAge.
func Check(path string, maxAge time.Duration, now time.Time) error
// CheckURL hace un GET a url con timeout de 3 s y exige 200.
func CheckURL(url string) error
```

`opennvr healthcheck --file /tmp/heartbeat --max-age 180s` o
`opennvr healthcheck --url http://127.0.0.1:8095/healthz` sale con 0 si
todo va bien y con 1 si no.

Equivalencias: `services/common/opennvr_common/{throttle,ntfy}.py` y sus
tests en `services/common/tests/{test_throttle,test_creds_ntfy}.py`.

- [ ] **Paso 1: portar los tests.** Cada `def test_*` de
  `services/common/tests/test_throttle.py` y `test_creds_ntfy.py` pasa a un
  `func Test*` con los mismos valores y aserciones. Los tests de redacción ya
  quedaron cubiertos en la Tarea 1. Añadir además:
  - `TestThrottleStateJSONCompat`: deserializa
    `{"last":{"down:tcp:80":1793008800.5},"sent":[1793008800.5],"suppressed":2}`,
    hace `Allow("down:tcp:80", 1793008801, -1)` → false y `TakeSuppressed()` → 3.
  - `TestHTTPSenderPostsJSON`: un `httptest.Server` comprueba la ruta `/`,
    `Content-Type: application/json`, `Authorization: Bearer tok` y el cuerpo.
  - `TestNotifierFailureRestoresThrottle`: un `Sender` que devuelve error
    hace que `Notify` devuelva false, la clave queda olvidada y se suman los
    `suppressed`.
  - `health`: `Beat` y `Check` con un archivo reciente y con uno antiguo
    (`os.Chtimes`).
- [ ] **Paso 2: comprobar que fallan.** Ejecutar `go test ./internal/notify/... ./internal/health/...`.
  Resultado esperado: FAIL.
- [ ] **Paso 3: implementar** traduciendo línea a línea `throttle.py` y
  `ntfy.py`. Diferencias permitidas:
  - `interval` negativo significa "por defecto", el `None` de Python.
  - Si `URL` está vacía, `Notify` registra con `logx.Printf("[ntfy desactivado] %s: %s", ...)`
    y devuelve true.
  - `Prune` recibe `maxAge` explícito.
  - `NewThrottle` inicializa los mapas nulos para que el estado vacío sea
    válido.
- [ ] **Paso 4: implementar `health`** y `cmd_healthcheck.go` con `flag.NewFlagSet`.
- [ ] **Paso 5: comprobar que pasan.** Ejecutar `go test ./...`. Resultado esperado: PASS.
- [ ] **Paso 6: commit**

```bash
git add internal/notify internal/health cmd/opennvr/cmd_healthcheck.go
git commit -m "feat(go): port notification throttle and ntfy client, add healthcheck"
```

### Tarea 3: port de nvr-watch (delegable a Codex)

**Archivos:**
- Crear: `internal/watch/nvrwatch/{config,xmlparse,baseline,availability,checks,ports,probes,app}.go`,
  `cmd/opennvr/cmd_nvrwatch.go`, `testdata/nvrwatch/*.xml`,
  `testdata/nvrwatch/python-state.json`, `testdata/nvrwatch/python-baseline.json`
- Test: un `*_test.go` por cada archivo de `services/nvr-watch/tests/`
  (`xmlparse_test.go`, `baseline_test.go`, `availability_checks_test.go`,
  `app_baseline_test.go`) más `compat_test.go`

**Interfaces:**
- Consume: `config.*`, `logx.Printf`, `statefile.*`, `notify.*`, `health.Beat`
- Produce: `nvrwatch.FromEnv() Config`, `nvrwatch.NewApp(cfg Config, n *notify.Notifier, clock func() float64, sleep func(time.Duration)) (*App, error)`,
  `(*App).Tick(now float64) error`, `(*App).RunForever(ctx context.Context)`,
  `nvrwatch.Accept(cfg Config) int`, `nvrwatch.Show(cfg Config) int`.
  Subcomando: `opennvr nvr-watch [run|once|accept|show]`, el mismo contrato
  que `python -m nvrwatch`.

**Fuente de verdad:** `services/nvr-watch/nvrwatch/*.py`. Se traduce archivo
a archivo con el mismo nombre en Go, conservando claves, textos de los
avisos (en español), intervalos y orden de las comprobaciones. Reglas:

1. **Estado compatible:** `state.json` tiene las claves `avail`, `throttle`,
   `scan` (`{"ts":float,"open":[int]}`), `last_scan` y `last_baseline`.
   `baseline.json` y `last_snapshot.json` usan `created`, `accepted`, `ts`,
   `sections` y `values` (`map[string]string`). Se usan structs con esas
   etiquetas JSON. Los campos desconocidos se conservan con un
   `map[string]json.RawMessage` auxiliar, o se documenta que no existen.
2. **Fixtures de compatibilidad generados con Python antes de borrarlo.** Hay
   que escribir `testdata/nvrwatch/gen_compat.py` (en scratch, no en el repo)
   que, con `PYTHONPATH=services/nvr-watch:services/common`, instancie
   `App` con `DATA_DIR` temporal y un `Notifier` falso. Debe llamar a
   `_apply`, `throttle.allow` y `compare_baseline` con los XML saneados,
   copiar los `state.json` y `baseline.json` resultantes a
   `testdata/nvrwatch/python-*.json`, y añadirlos al repo. `compat_test.go`
   los carga con el código Go, ejecuta un `Tick` con un NVR falso
   (`httptest`) y comprueba que no hay error y que las claves se conservan.
3. **XML saneados:** se copian de `captures/api/` los XML que usan los tests
   Python, sustituyendo `SerialNum`, `ChipID`, `DID`/UID, `MAC` y
   cualquier contraseña por valores ficticios (`N0000000000000000000000`,
   `00:11:22:33:44:5X`, `XXXXXX-000000-XXXXX`). Se verifica con
   un `grep` con el número de serie, el ChipID, el DID y las MAC reales (tomados de `captures/`, sin escribirlos en el repo), que no debe
   devolver nada.
4. **Sondas:** `probes.py` pasa a `net.DialTimeout`, `net/http` con Basic y
   un DESCRIBE RTSP escrito a mano sobre TCP (mismos timeouts y misma lógica
   Digest/Basic que `probes.py` y `tools/rtsp_describe.py`). En los tests,
   las sondas se sustituyen por funciones inyectables
   (`type Probes interface{ TCP(host string, port int) (bool, string); HTTPGet(...); RTSPDescribe(...); Scan(...) }`).
5. **Corrección de comportamiento (única permitida):** `camera_checks`
   considera conectado solo `connect success`, pero las 4 cámaras reales
   informan `weak password` estando en línea (T-logs del 2026-10-27). En Go
   se consideran conectadas `connect success` y `weak password`, con el
   test `TestCameraWeakPasswordIsOnline`. Se anota en el mensaje de commit.
6. **Bucle:** `RunForever` respeta `ctx` (`SIGTERM` lo cancela), llama a
   `health.Beat("/tmp/heartbeat")` en cada ciclo y nunca muere por un error
   de `Tick` (`logx.Printf("error en el ciclo: %v", err)`).
7. El texto de ayuda de `accept` cambia a
   `podman exec opennvr-nvr-watch /opennvr nvr-watch accept`.

- [ ] **Paso 1:** generar los fixtures saneados y los de compatibilidad
  (reglas 2 y 3). Commit `test(nvrwatch): add sanitized fixtures and Python state snapshots`.
- [ ] **Paso 2:** portar los tests de `services/nvr-watch/tests/*.py`, uno
  a uno, con los mismos casos, más `compat_test.go` y
  `TestCameraWeakPasswordIsOnline`. Ejecutar `go test ./internal/watch/nvrwatch/`.
  Resultado esperado: FAIL.
- [ ] **Paso 3:** implementar los paquetes en este orden: `xmlparse`,
  `baseline`, `availability`, `checks`, `ports`, `probes`, `config` y `app`.
  Ejecutar `go test` después de cada archivo.
- [ ] **Paso 4:** `cmd_nvrwatch.go` con `run|once|accept|show`. `run`
  instala `signal.NotifyContext(ctx, SIGTERM, SIGINT)`.
- [ ] **Paso 5:** `go vet ./... && go test -race ./...`. Resultado esperado: PASS.
  Comparar el número de tests con los de Python: hay que listar
  `grep -c "def test_" services/nvr-watch/tests/*.py` y cada Python debe
  tener al menos un `t.Run` o `Test*` equivalente. La tabla de
  correspondencias va en la descripción del commit.
- [ ] **Paso 6:** commit `feat(nvrwatch): port nvr-watch to Go`.

### Tarea 4: port de egress-watch (delegable a Codex, en paralelo con la Tarea 3)

**Archivos:**
- Crear: `internal/watch/egresswatch/{parse,tail,aggregate,watch}.go`,
  `cmd/opennvr/cmd_egresswatch.go`, `testdata/egresswatch/python-state.json`
- Test: `parse_test.go`, `tail_test.go`, `aggregate_test.go`, `compat_test.go`

**Interfaces:**
- Consume: igual que la Tarea 3
- Produce: `egresswatch.LoadConfig() Config`,
  `egresswatch.New(cfg Config, clock func() float64, today func() time.Time) (*Watch, error)`,
  `(*Watch).Poll() error`, `(*Watch).RunForever(ctx)`,
  `(*Watch).Summary() string`. Subcomando:
  `opennvr egress-watch [run|summary]`.

**Fuente de verdad:** `services/egress-watch/egresswatch/*.py` y sus tests.
Mismas reglas que la Tarea 3, con estas diferencias:

1. El estado tiene las claves `agg`, `throttle` y `tails`
   (`{ruta: {"inode":..,"offset":..}}`; comprobar los nombres exactos en
   `tail.py`). Hay que generar `python-state.json` haciendo pasar por
   `EgressWatch.poll()` de Python unas líneas de ejemplo de ulogd y dnsmasq,
   que están en `services/egress-watch/tests/`.
2. Las líneas de ejemplo de los tests contienen IP públicas de servicios
   (S17), no datos personales, así que se pueden copiar tal cual.
3. `RunForever` llama a `health.Beat("/tmp/heartbeat")` en cada sondeo.
4. La conversión `datetime.datetime.now()` → `today func() time.Time` se
   inyecta para los tests del resumen diario.

- [ ] **Paso 1:** generar `python-state.json` y portar los tests. Ejecutar
  `go test ./internal/watch/egresswatch/`. Resultado esperado: FAIL.
- [ ] **Paso 2:** implementar `parse`, `tail`, `aggregate` y `watch`, en ese orden.
- [ ] **Paso 3:** `cmd_egresswatch.go` con `run|summary`.
- [ ] **Paso 4:** `go vet ./... && go test -race ./...`. Resultado esperado: PASS,
  con la tabla de correspondencias como en la Tarea 3.
- [ ] **Paso 5:** commit `feat(egresswatch): port egress-watch to Go`.

### Tarea 5: imagen única, compose, CI y eliminación de Python

**Archivos:**
- Crear: `Dockerfile`
- Modificar: `deploy/baphomet/compose.yaml` (nvr-watch, egress-watch),
  `deploy/baphomet/compose.build.yaml`, `.github/workflows/images.yml`,
  `deploy/baphomet/README.md`, `README.md`, `docs/monitoring.md`,
  `docs/superpowers/specs/2026-10-27-web-interface-design.md` (base busybox)
- Borrar: `services/`

**Interfaces:**
- Consume: los subcomandos de las Tareas 2–4
- Produce: imagen `ghcr.io/belcaik/open-nvr:{latest,sha-<corto>}` con
  `/opennvr` como `ENTRYPOINT`.

- [ ] **Paso 1: Dockerfile**

```dockerfile
# Imagen única: el subcomando se elige con `command:` en compose.
FROM docker.io/library/golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd cmd
COPY internal internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/opennvr ./cmd/opennvr

FROM docker.io/library/busybox:1.36.1-musl
COPY --from=build /out/opennvr /opennvr
VOLUME ["/data"]
ENTRYPOINT ["/opennvr"]
```

  Antes de fijar las etiquetas, comprobar que existen con
  `docker manifest inspect docker.io/library/golang:1.24-alpine` y
  `docker manifest inspect docker.io/library/busybox:1.36.1-musl`. Fijar el
  `golang` a la última versión de parche 1.24 que exista (por ejemplo
  `1.24.6-alpine`), nunca a una etiqueta flotante.

- [ ] **Paso 2: comprobar la imagen en local**

```bash
docker build -t opennvr:test --build-arg VERSION=test .
docker run --rm opennvr:test version          # test
docker image ls opennvr:test --format '{{.Size}}'   # < 20 MB
docker run --rm -e NVR_HOST=127.0.0.1 -e CHECK_INTERVAL=5 --entrypoint sh opennvr:test \
  -c '/opennvr nvr-watch once; ls /tmp'      # no debe abortar
```

- [ ] **Paso 3: compose.** En `nvr-watch` y `egress-watch`:

```yaml
    image: ghcr.io/belcaik/open-nvr:${OPENNVR_TAG:-latest}
    command: ["nvr-watch", "run"]          # egress-watch: ["egress-watch", "run"]
    healthcheck:
      test: ["CMD-SHELL", "/opennvr healthcheck --file /tmp/heartbeat --max-age 300s"]
```

  En egress-watch, `--max-age 60s`, porque sondea cada 5 s. Quitar sus
  entradas `build` de `compose.build.yaml` y añadir un único servicio
  `build: {context: ../.., dockerfile: Dockerfile}` en `nvr-watch` y
  `egress-watch`.

- [ ] **Paso 4: verificar con podman-compose 1.0.6 (dry run)**

```bash
SP=/home/belcaik/.tmp/claude-1000/-home-belcaik-Dev-open-nvr/da387a3e-8a3e-4f2c-8bae-7239bb2ea6c1/scratchpad
PATH=$SP/fakebin:$PATH $SP/pc106/bin/podman-compose -f deploy/baphomet/compose.yaml --dry-run up -d 2>&1 \
  | grep -E 'health-cmd|open-nvr:' 
```

  Resultado esperado: `--healthcheck-command /bin/sh -c '/opennvr healthcheck ...'`
  sin argumentos partidos ni comillas sobrantes. Si no existe el entorno de
  prueba, se recrea con `python -m venv $SP/pc106 && $SP/pc106/bin/pip install podman-compose==1.0.6`
  y un `fakebin/podman` que hace `echo "$@"`.

- [ ] **Paso 5: CI.** Sustituir el job `test` por:

```yaml
  test:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test -race ./...
```

  En la matriz `images`, sustituir `nvr-watch` y `egress-watch` por
  `{name: "", context: ., dockerfile: Dockerfile}` y ajustar
  `images: ghcr.io/${{ github.repository_owner }}/open-nvr${{ matrix.name && format('-{0}', matrix.name) || '' }}`.
  Pasar `build-args: VERSION=${{ github.sha }}`.

- [ ] **Paso 6: borrar Python y actualizar la documentación.** `git rm -r services`.
  En los README y en `monitoring.md`, cambiar `python -m nvrwatch ...` por
  `/opennvr nvr-watch ...` y las imágenes por `open-nvr`. En la spec, en la
  sección Despliegue, la base pasa a ser busybox. Hay que comprobar que no
  queda nada con `grep -rn "python -m\|open-nvr-nvr-watch\|open-nvr-egress-watch\|services/" --include=*.md --include=*.yml --include=*.yaml .`,
  excepto los registros históricos de `docs/test-log.md`.
- [ ] **Paso 7: comprobación final.** `go test -race ./...` y el dry run del
  paso 4. Commits separados:
  - `build: add single Go image for all opennvr subcommands`
  - `feat(deploy): run watchers from the Go image`
  - `ci: test Go and publish the single opennvr image`
  - `refactor!: remove the Python services replaced by Go`
  - `docs: update commands and images after the Go migration`

### Tarea 6: despliegue de la fase 1 en Baphomet (requiere al propietario)

Los pasos con `sudo` los ejecuta el propietario. La sesión principal prepara
los comandos y verifica el resultado por SSH sin sudo.

- [ ] **Paso 1:** `git push` y esperar a que CI publique `open-nvr:latest`
  (`gh run watch`), y comprobar el pull anónimo:
  `curl -s -o /dev/null -w '%{http_code}' https://ghcr.io/v2/belcaik/open-nvr/manifests/latest`
  con un token anónimo, que debe devolver 200, igual que en el despliegue
  anterior.
- [ ] **Paso 2 (propietario):**

```bash
cd /opt/open-nvr && sudo git pull
cd deploy/baphomet && sudo podman-compose pull nvr-watch egress-watch
sudo podman-compose up -d nvr-watch egress-watch
sudo podman ps --format '{{.Names}} {{.Image}} {{.Status}}'
```

- [ ] **Paso 3: verificar la paridad.** Comandos:
  - `sudo podman logs --tail 30 opennvr-nvr-watch`: arranque y ciclo, sin
    errores de JSON con el estado anterior.
  - `sudo podman exec opennvr-nvr-watch /opennvr nvr-watch show`: debe
    mostrar `sin cambios` o los mismos cambios que antes.
  - `sudo podman exec opennvr-egress-watch /opennvr egress-watch summary`:
    deben aparecer los mismos destinos S17.
  - Pasados 2 minutos, los dos contenedores deben estar `healthy`.

  Si falla, revertir con `OPENNVR_TAG=<sha anterior>`. Las imágenes antiguas
  `open-nvr-nvr-watch:sha-0703d1f` siguen en GHCR, pero el compose nuevo
  usa el comando de Go, así que la reversión exige el commit anterior:
  `git checkout 0703d1f -- deploy/baphomet/compose.yaml`.
- [ ] **Paso 4:** apuntar T70 en `docs/test-log.md` y hacer commit
  `docs(tests): verify Go watchers on Baphomet`.

---

## Fase 2: interfaz web en directo con PTZ

### Tarea 7: cliente HTTP del NVR (delegable a Kimi o Codex)

**Archivos:**
- Crear: `internal/nvr/client.go`, `internal/nvr/device.go`,
  `internal/nvr/ptz.go`, `internal/nvr/snapshot.go`, `testdata/nvr/*.xml`
- Test: `internal/nvr/client_test.go`, `device_test.go`, `ptz_test.go`

**Interfaces:**
- Produce:

```go
package nvr

var (
	ErrUnauthorized = errors.New("nvr: credenciales rechazadas")
	ErrUnavailable  = errors.New("nvr: no responde")
)
// BadResponseError: HTTP distinto de 200 o statusCode distinto de 0.
type BadResponseError struct {
	Path   string
	HTTP   int
	Status string // statusCode del XML ResponseStatus, "" si no lo hay
}
func (e *BadResponseError) Error() string

type Client struct{ /* base, user, pass, hc *http.Client, sem chan struct{} */ }
// New: base "http://host:port". Como mucho 2 peticiones simultáneas.
func New(base, user, pass string) *Client
// Timeouts: 5 s para control y lectura, 15 s para Snapshot.

type DeviceInfo struct {
	Name, Model, Firmware string
	VideoChannels         int
	TwoWayAudio           bool
}
func (c *Client) DeviceInfo(ctx context.Context) (DeviceInfo, error)

type Camera struct {
	Channel     int    // 1..N
	IP          string
	Model       string // DevType
	Firmware    string // SoftwareVersion
	LoginStatus string // texto crudo de FrontConnectDeviceStatusList
	Online      bool   // LoginStatus es "connect success" o "weak password"
}
// Cameras une FrontConnectDevicetList y FrontConnectDeviceStatusList
// (la lista de estado va por índice: elemento i = canal i+1). Omite los
// canales sin IP ni MAC, igual que xmlparse.cameras.
func (c *Client) Cameras(ctx context.Context) ([]Camera, error)

type PTZCommand string
const (
	TurnUp PTZCommand = "TurnUp"; TurnDown = "TurnDown"; TurnLeft = "TurnLeft"; TurnRight = "TurnRight"
	TurnUpLeft = "TurnUpLeft"; TurnUpRight = "TurnUpRight"; TurnDownLeft = "TurnDownLeft"; TurnDownRight = "TurnDownRight"
	ZoomIn = "ZoomIn"; ZoomOut = "ZoomOut"; FocusNear = "FocusNear"; FocusFar = "FocusFar"
)
func ValidPTZCommand(s string) (PTZCommand, bool)
// PTZ: PUT /PTZ/{ch}/{cmd} con cuerpo "Param1=1&Param2={speed}" (on) o "Param1=0&Param2={speed}".
// speed se limita a 1..10.
func (c *Client) PTZ(ctx context.Context, ch int, cmd PTZCommand, on bool, speed int) error
type PresetAction string
const (PresetGoto PresetAction = "Goto"; PresetSet = "Set"; PresetRemove = "Remove")
// Preset: PUT /PTZ/{ch}/Presets/{action}, cuerpo "Param1={idx}" con idx 0..255.
func (c *Client) Preset(ctx context.Context, ch int, action PresetAction, idx int) error
// Light y Wiper: PUT /PTZ/{ch}/Light|RainBrush, cuerpo "Param1=1|0".
func (c *Client) Light(ctx context.Context, ch int, on bool) error
func (c *Client) Wiper(ctx context.Context, ch int, on bool) error
// Snapshot: GET /Snapshot/{ch}/RemoteImageCapture?ImageFormat=2 → JPEG.
func (c *Client) Snapshot(ctx context.Context, ch int) ([]byte, error)
```

Códigos: 401 → `ErrUnauthorized`. Un error de red o un timeout →
`ErrUnavailable`, envuelto con `%w`. Un cuerpo PUT `<ResponseStatus>` con
`statusCode` distinto de `0` → `*BadResponseError`. Si el NVR devuelve HTML
`Not Found` con 200, también es `*BadResponseError` (caso de
`System_PTZCap.xml`). `Content-Type` del PUT:
`application/x-www-form-urlencoded`.

- [ ] **Paso 1: fixtures.** Copiar a `testdata/nvr/` las versiones
  saneadas de `System_DeviceInfo.xml` (`SerialNum` y `ChipID` ficticios,
  conservando el resto), `FrontConnect-4cams.xml` (`MAC`, `DID` y
  `Password` ficticios), `FrontConnectStatus-4cams.xml`, `System_LightCap_1.xml`
  (ejemplo de `statusCode -1`) y `System_PTZCap.xml` (HTML Not Found).
  Crear `ok.xml` con `<ResponseStatus Version="1.0"><requestURL>/PTZ/1/TurnLeft</requestURL><statusCode>0</statusCode></ResponseStatus>`.
  Comprobar con el `grep` de la Tarea 3.
- [ ] **Paso 2: tests.** Un `httptest.Server` que enruta por
  `r.Method + " " + r.URL.Path` y registra las peticiones recibidas. Casos:
  - `TestDeviceInfo`: el modelo es `N310B-11NEA-AI-Z02-P4`, 10 canales y
    `TwoWayAudio` true. También se comprueba que se envía Basic auth.
  - `TestCamerasJoinsStatus`: 4 cámaras, canales 1–4, `Online` true
    (weak password) y el modelo `W3CA-52PHM-S/W/BT`.
  - `TestPTZBodyAndSpeedClamp`: `PTZ(ctx,2,TurnLeft,true,15)` envía
    `PUT /PTZ/2/TurnLeft` con cuerpo `Param1=1&Param2=10`. Con `on=false`
    envía `Param1=0&Param2=10`.
  - `TestValidPTZCommandRejectsUnknown`: `"AutoLeftRight"`,
    `"ApertureBigger"` y `"../x"` → false.
  - `TestPresetRange`: `idx` -1 o 256 → error sin petición HTTP.
  - `TestStatusCodeNonZero`: la respuesta `LightCap` (-1) →
    `*BadResponseError` con `Status=="-1"`.
  - `TestHTMLNotFound` → `*BadResponseError`.
  - `TestUnauthorized`: 401 → `errors.Is(err, ErrUnauthorized)`.
  - `TestUnavailable`: servidor cerrado → `errors.Is(err, ErrUnavailable)`.
  - `TestConcurrencyLimit`: un handler que bloquea y cuenta las peticiones
    simultáneas. Con 5 goroutines llamando a `DeviceInfo`, el máximo
    observado es 2.
  - `TestSnapshotReturnsJPEG`: un cuerpo `\xff\xd8...` se devuelve tal cual.
- [ ] **Paso 3:** `go test ./internal/nvr/`. Resultado esperado: FAIL.
- [ ] **Paso 4: implementar.** `client.go` contiene `do(ctx, method, path, body string, timeout time.Duration) ([]byte, error)`,
  con el semáforo `sem <- struct{}{}` respetando `ctx`, `context.WithTimeout`
  y `req.SetBasicAuth`. El XML se parsea con `encoding/xml` y structs
  mínimos.
- [ ] **Paso 5:** `go test -race ./internal/nvr/`. Resultado esperado: PASS.
- [ ] **Paso 6:** commit `feat(nvr): add typed HTTP client for device, cameras, PTZ and snapshots`.

### Tarea 8: stop de seguridad de PTZ (sesión principal)

**Archivos:**
- Crear: `internal/camera/ptzguard.go`
- Test: `internal/camera/ptzguard_test.go`

**Interfaces:**
- Consume: `nvr.PTZCommand`, y la firma de `(*nvr.Client).PTZ` a través de
  una interfaz
- Produce:

```go
package camera

type Mover interface {
	PTZ(ctx context.Context, ch int, cmd nvr.PTZCommand, on bool, speed int) error
}
type motion struct {
	cmd      nvr.PTZCommand
	speed    int
	deadline time.Time
}
type Guard struct { /* mv Mover; timeout time.Duration; mu sync.Mutex; active map[int]motion; now func() time.Time */ }
// NewGuard: timeout = 1500 ms en producción.
func NewGuard(mv Mover, timeout time.Duration, now func() time.Time) *Guard
// Start detiene antes cualquier movimiento activo en ch y arranca cmd.
func (g *Guard) Start(ctx context.Context, ch int, cmd nvr.PTZCommand, speed int) error
// Keepalive prolonga el plazo; devuelve false si no hay movimiento activo.
func (g *Guard) Keepalive(ch int) bool
// Stop envía Param1=0 al comando activo (3 intentos) y lo olvida.
func (g *Guard) Stop(ctx context.Context, ch int) error
// Expire detiene los movimientos cuyo plazo ya venció en `now`.
func (g *Guard) Expire(ctx context.Context)
// Run llama a Expire cada 250 ms hasta que ctx termina y entonces llama a StopAll.
func (g *Guard) Run(ctx context.Context)
func (g *Guard) StopAll(ctx context.Context)
```

- [ ] **Paso 1: tests con reloj falso y un `fakeMover` que registra las llamadas**

```go
package camera

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
)

type call struct {
	ch  int
	cmd nvr.PTZCommand
	on  bool
}

type fakeMover struct {
	mu    sync.Mutex
	calls []call
	fail  int // número de fallos antes de responder bien
}

func (f *fakeMover) PTZ(_ context.Context, ch int, cmd nvr.PTZCommand, on bool, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{ch, cmd, on})
	if f.fail > 0 {
		f.fail--
		return errors.New("fallo")
	}
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup() (*Guard, *fakeMover, *clock) {
	m := &fakeMover{}
	c := &clock{t: time.Unix(1000, 0)}
	return NewGuard(m, 1500*time.Millisecond, c.now), m, c
}

func TestExpireStopsWithoutKeepalive(t *testing.T) {
	g, m, c := setup()
	ctx := context.Background()
	g.Start(ctx, 1, nvr.TurnLeft, 5)
	c.t = c.t.Add(1400 * time.Millisecond)
	g.Expire(ctx)
	if len(m.calls) != 1 {
		t.Fatalf("paró antes de tiempo: %v", m.calls)
	}
	c.t = c.t.Add(200 * time.Millisecond)
	g.Expire(ctx)
	if got := m.calls[len(m.calls)-1]; got != (call{1, nvr.TurnLeft, false}) {
		t.Fatalf("última llamada = %v", got)
	}
	if g.Keepalive(1) {
		t.Fatal("sigue activo tras expirar")
	}
}

func TestKeepaliveExtends(t *testing.T) {
	g, m, c := setup()
	ctx := context.Background()
	g.Start(ctx, 1, nvr.TurnUp, 5)
	for i := 0; i < 10; i++ {
		c.t = c.t.Add(500 * time.Millisecond)
		if !g.Keepalive(1) {
			t.Fatal("keepalive rechazado")
		}
		g.Expire(ctx)
	}
	if len(m.calls) != 1 {
		t.Fatalf("no debía parar: %v", m.calls)
	}
}

func TestStartReplacesActiveMotion(t *testing.T) {
	g, m, _ := setup()
	ctx := context.Background()
	g.Start(ctx, 1, nvr.TurnLeft, 5)
	g.Start(ctx, 1, nvr.TurnRight, 5)
	want := []call{{1, nvr.TurnLeft, true}, {1, nvr.TurnLeft, false}, {1, nvr.TurnRight, true}}
	if len(m.calls) != 3 || m.calls[0] != want[0] || m.calls[1] != want[1] || m.calls[2] != want[2] {
		t.Fatalf("llamadas = %v", m.calls)
	}
}

func TestStopRetriesThreeTimes(t *testing.T) {
	g, m, _ := setup()
	ctx := context.Background()
	g.Start(ctx, 2, nvr.ZoomIn, 5)
	m.fail = 2
	if err := g.Stop(ctx, 2); err != nil {
		t.Fatalf("Stop = %v", err)
	}
	if len(m.calls) != 4 { // start + 2 fallos + 1 éxito
		t.Fatalf("llamadas = %v", m.calls)
	}
}

func TestStopAllOnShutdown(t *testing.T) {
	g, m, _ := setup()
	ctx, cancel := context.WithCancel(context.Background())
	g.Start(context.Background(), 1, nvr.TurnLeft, 5)
	g.Start(context.Background(), 3, nvr.ZoomOut, 5)
	done := make(chan struct{})
	go func() { g.Run(ctx); close(done) }()
	cancel()
	<-done
	stops := 0
	for _, c := range m.calls {
		if !c.on {
			stops++
		}
	}
	if stops != 2 {
		t.Fatalf("stops = %d (%v)", stops, m.calls)
	}
}
```

- [ ] **Paso 2:** `go test ./internal/camera/`. Resultado esperado: FAIL.
- [ ] **Paso 3: implementar.** Las llamadas al NVR se hacen fuera del mutex:
  se copia `motion`, se borra de `active` y se desbloquea antes de llamar a
  `Stop`. `StopAll` usa un `context.WithTimeout(context.Background(), 3*time.Second)`
  propio, porque el `ctx` de `Run` ya está cancelado. Los reintentos de
  `Stop` no esperan entre intentos más de 100 ms.
- [ ] **Paso 4:** `go test -race ./internal/camera/`. Resultado esperado: PASS.
- [ ] **Paso 5:** commit `feat(camera): add PTZ guard that stops motion without keepalive`.

### Tarea 9: autenticación (sesión principal)

**Archivos:**
- Crear: `internal/auth/password.go`, `session.go`, `csrf.go`,
  `ratelimit.go`, `cmd/opennvr/cmd_hashpassword.go`
- Test: `internal/auth/*_test.go`

**Interfaces:**
- Produce:

```go
package auth

// HashPassword genera "$argon2id$v=19$m=65536,t=3,p=2$<salt b64>$<hash b64>" (formato PHC).
func HashPassword(pw string) (string, error)
// VerifyPassword compara en tiempo constante. Un hash mal formado devuelve false.
func VerifyPassword(encoded, pw string) bool

type Session struct {
	User     string
	ID       string // 16 bytes aleatorios en base64url
	Issued   time.Time
	LastSeen time.Time
}
type Sessions struct {
	Key     []byte        // >= 32 bytes, secreto web_session_key
	Idle    time.Duration // 7 días
	MaxAge  time.Duration // 30 días
	Refresh time.Duration // 1 hora
	Now     func() time.Time
}
const CookieName = "opennvr_session"
// New crea una sesión nueva para user.
func (s *Sessions) New(user string) (Session, error)
// Encode: base64url("v1|user|id|issuedUnix|lastUnix") + "." + base64url(HMAC-SHA256).
func (s *Sessions) Encode(se Session) string
// Decode valida la firma, la versión y la caducidad (Idle y MaxAge).
func (s *Sessions) Decode(v string) (Session, error)
// NeedsRefresh: Now-LastSeen >= Refresh.
func (s *Sessions) NeedsRefresh(se Session) bool
// Cookie: HttpOnly, Secure, SameSite=Strict, Path=/, MaxAge = Idle en segundos.
func (s *Sessions) Cookie(se Session) *http.Cookie
func (s *Sessions) ClearCookie() *http.Cookie

// CSRFToken = base64url(HMAC-SHA256(Key, "csrf|"+se.ID)).
func (s *Sessions) CSRFToken(se Session) string
func (s *Sessions) CheckCSRF(se Session, token string) bool

type Limiter struct { /* Max=5, Window=15m, Now, mu, fails map[string][]time.Time */ }
func NewLimiter(max int, window time.Duration, now func() time.Time) *Limiter
func (l *Limiter) Blocked(ip string) bool
func (l *Limiter) Fail(ip string)
func (l *Limiter) Reset(ip string)
```

`opennvr hash-password` lee la contraseña de stdin (sin eco si es una
TTY, con `golang.org/x/term`, que es parte de `x/` y se acepta junto a
`x/crypto`) y muestra el hash. También existe `opennvr hash-password --gen-key`,
que muestra 32 bytes aleatorios en base64.

- [ ] **Paso 1: tests**
  - `TestHashVerify`: la contraseña correcta da true, la incorrecta false y
    un hash corrupto false. Dos hashes de la misma contraseña son distintos
    (por la sal).
  - `TestSessionRoundTrip`: `Decode(Encode(se))` es igual a `se`, con la
    precisión en segundos.
  - `TestSessionTamper`: cambiar un byte del payload o de la firma da error.
    Una clave distinta da error.
  - `TestSessionIdleExpiry`: con `LastSeen` hace 7 días y 1 segundo, error.
  - `TestSessionMaxAge`: con `Issued` hace 30 días y 1 segundo, error
    aunque `LastSeen` sea reciente.
  - `TestSessionSurvivesNewInstance`: dos `Sessions` con la misma `Key`
    (simula un reinicio) aceptan la misma cookie.
  - `TestNeedsRefresh`: con `LastSeen` hace 59 minutos, false; con 61, true.
  - `TestCookieFlags`: `HttpOnly`, `Secure` y `SameSite == http.SameSiteStrictMode`.
  - `TestCSRF`: el token de la sesión A no vale para la sesión B.
  - `TestLimiter`: tras 5 fallos queda bloqueado; a los 15 minutos y 1
    segundo se desbloquea; `Reset` lo desbloquea.
- [ ] **Paso 2:** `go get golang.org/x/crypto/argon2 golang.org/x/term`, y
  `go test ./internal/auth/`. Resultado esperado: FAIL.
- [ ] **Paso 3: implementar.** Comparaciones con `hmac.Equal` y
  `subtle.ConstantTimeCompare`. El usuario no puede contener `|`, se valida
  en `New`.
- [ ] **Paso 4:** `go test -race ./internal/auth/`. Resultado esperado: PASS.
- [ ] **Paso 5:** commit `feat(auth): add argon2id passwords, persistent signed sessions, CSRF and login limiter`.

### Tarea 10: integración con go2rtc (sesión principal)

**Archivos:**
- Crear: `internal/media/go2rtc.go`, `internal/media/proxy.go`
- Test: `internal/media/go2rtc_test.go`, `internal/media/proxy_test.go`

**Interfaces:**
- Produce:

```go
package media

type Config struct {
	NVRHost  string // 192.168.0.227
	RTSPPort int    // 554
	User     string
	Pass     string
	Channels []int  // 1..4
	APIListen    string // "127.0.0.1:1984"
	WebRTCListen string // ":8555"
	Candidate    string // "192.168.0.8:8555"
	VAAPI        bool
}
// StreamName: cam1, cam1_sub, cam1_h264, cam1_sub_h264.
func StreamName(ch int, sub, h264 bool) string
// Render devuelve el YAML de go2rtc (texto determinista, sin dependencias YAML).
func Render(c Config) string
// WriteConfig escribe Render en path con modo 0600 de forma atómica.
func WriteConfig(path string, c Config) error

// Proxy reenvía /api/stream/ws?src=<stream> a ws://APIListen/api/ws?src=<stream>.
// Rechaza (404) los src que no estén en la lista permitida.
type Proxy struct{ /* allowed map[string]bool; rp *httputil.ReverseProxy */ }
func NewProxy(apiListen string, channels []int) *Proxy
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

YAML esperado para `Channels: [1]` con `VAAPI: true`:

```yaml
api:
  listen: "127.0.0.1:1984"
  origin: "*"
rtsp:
  listen: ""
webrtc:
  listen: ":8555"
  candidates:
    - "192.168.0.8:8555"
log:
  level: warn
streams:
  cam1: "rtsp://USER:PASS@192.168.0.227:554/live/1/MAIN#transport=tcp"
  cam1_sub: "rtsp://USER:PASS@192.168.0.227:554/live/1/SUB#transport=tcp"
  cam1_h264: "ffmpeg:cam1#video=h264#hardware=vaapi#audio=opus"
  cam1_sub_h264: "ffmpeg:cam1_sub#video=h264#hardware=vaapi#audio=opus"
```

El usuario y la contraseña se escapan con `url.UserPassword(...).String()`.
`origin: "*"` es aceptable porque la API solo escucha en loopback y la app
web valida la sesión y el `Origin` antes de reenviar.

- [ ] **Paso 1: tests**
  - `TestRenderGolden`: compara con el YAML de arriba, con
    `User="admin"` y `Pass="p@ss:w/rd"`, que debe salir como
    `admin:p%40ss%3Aw%2Frd`.
  - `TestRenderNoVAAPI`: sin `#hardware=vaapi`.
  - `TestWriteConfigMode`: modo 0600.
  - `TestProxyRejectsUnknownSrc`: `src=cam9` o `src=rtsp://x` → 404, y el
    backend no recibe nada.
  - `TestProxyForwardsWebSocket`: un backend `httptest` que responde 101 a
    `Upgrade: websocket` y hace eco. Se usa un cliente WebSocket mínimo
    escrito a mano en el test (handshake HTTP más un frame de texto sin
    máscara del servidor) para no añadir dependencias. Hay que comprobar
    que el backend recibió la ruta `/api/ws?src=cam1_sub`.
- [ ] **Paso 2:** `go test ./internal/media/`. Resultado esperado: FAIL.
- [ ] **Paso 3: implementar.** `httputil.ReverseProxy` con un `Rewrite` que
  fija `URL.Scheme="http"`, `Host=APIListen`, `Path="/api/ws"` y la query
  `src`, y borra `Cookie` y `Authorization` antes de reenviar.
- [ ] **Paso 4:** `go test -race ./internal/media/`. Resultado esperado: PASS.
- [ ] **Paso 5:** commit `feat(media): generate go2rtc config and proxy stream signaling`.

### Tarea 11: servidor web, middleware y login (sesión principal)

**Archivos:**
- Crear: `internal/camera/service.go`, `internal/web/server.go`,
  `internal/web/middleware.go`, `internal/web/handlers/{login,health}.go`,
  `internal/web/templates/{layout,login}.html`,
  `internal/web/static/{app.css,vendor/htmx.min.js}`,
  `internal/web/embed.go`, `cmd/opennvr/cmd_web.go`
- Test: `internal/web/server_test.go`

**Interfaces:**
- Consume: `auth.*` (Tarea 9), `media.*` (Tarea 10), `camera.Guard`
  (Tarea 8), `nvr.Client` (Tarea 7)
- Produce:

```go
package camera
type Service struct {
	NVR   *nvr.Client
	Guard *Guard
}
func (s *Service) Cameras(ctx context.Context) ([]nvr.Camera, error)  // caché de 30 s
func (s *Service) Health(ctx context.Context) error                   // DeviceInfo con timeout de 3 s

package web
type Deps struct {
	Cameras   *camera.Service
	Sessions  *auth.Sessions
	Limiter   *auth.Limiter
	PassHash  string
	User      string // "admin"
	Media     *media.Proxy
	Go2RTCAPI string // para /api/health
	TrustedProxies []netip.Prefix // de dónde se acepta X-Forwarded-For
}
func NewServer(d Deps) http.Handler
```

Rutas (Go 1.22 `ServeMux` con patrones de método):

| Ruta | Autenticación | Descripción |
|---|---|---|
| `GET /healthz` | no | 200 "ok" (lo usa el healthcheck del contenedor) |
| `GET /login`, `POST /login` | no (POST con límite de intentos) | formulario y verificación |
| `POST /logout` | sí + CSRF | borra la cookie |
| `GET /static/` | no | archivos embebidos, `Cache-Control: max-age=3600` |
| `GET /` | sí | cuadrícula (Tarea 12) |
| `GET /camera/{ch}` | sí | vista de una cámara (Tarea 12) |
| `GET /camera/{ch}/snapshot` | sí | JPEG (Tarea 12) |
| `GET /api/stream/ws` | sí, más `Origin` del mismo host | proxy de go2rtc |
| `POST /api/ptz/{ch}/{start,keepalive,stop}` | sí + CSRF | Tarea 13 |
| `POST /api/ptz/{ch}/preset/{action}/{idx}` | sí + CSRF | Tarea 13 |
| `POST /api/ptz/{ch}/{light,wiper}/{on,off}` | sí + CSRF | Tarea 13 |
| `GET /api/health` | sí | parcial htmx con el estado del NVR y de go2rtc |

Middleware, en orden: `securityHeaders` → `privateOnly` → `session` →
`csrf` (métodos distintos de GET y HEAD) → handler.

- `securityHeaders`:
  `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' wss:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`,
  `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`.
- `privateOnly`: la IP cliente (`RemoteAddr` o, si viene de un proxy de
  `TrustedProxies`, el último valor de `X-Forwarded-For`) debe ser
  privada, loopback o de enlace local. Si no, 403.
- `session`: sin cookie válida, un GET de página redirige 303 a `/login`;
  un `/api/*` o una petición htmx (`HX-Request`) devuelve 401 con la
  cabecera `HX-Redirect: /login`. Si `NeedsRefresh`, se reescribe la
  cookie con `LastSeen=Now`. La sesión se guarda en el contexto.
- `csrf`: la cabecera `X-CSRF-Token` o el campo de formulario
  `csrf_token` deben coincidir. Si no, 403.
- En `layout.html`, `<body hx-headers='{"X-CSRF-Token":"{{.CSRF}}"}'>`
  (htmx lo lee del atributo, sin JS en línea).
- Login: tras un fallo, `Limiter.Fail` y una espera fija de 500 ms. Si
  `Blocked`, se responde 429 sin verificar la contraseña.

`cmd_web.go` lee:
- `NVR_HOST`, `NVR_HTTP_PORT`, `NVR_RTSP_PORT`, `NVR_USER`,
  `Secret("NVR_PASS")`
- `WEB_LISTEN` (por defecto `127.0.0.1:8095`), `WEB_USER` (`admin`),
  `Secret("WEB_PASSWORD_HASH")`, `Secret("WEB_SESSION_KEY")` (base64, 32
  bytes o más; si no, sale con error)
- `WEB_TRUSTED_PROXIES` (por defecto `127.0.0.1/32,192.168.0.8/32`),
  `CAMERA_CHANNELS` (`1,2,3,4`)
- `GO2RTC_CONFIG` (`/config/go2rtc.yaml`), `GO2RTC_API` (`127.0.0.1:1984`),
  `GO2RTC_CANDIDATE` (`192.168.0.8:8555`), `GO2RTC_VAAPI` (`true`)

Al arrancar, `cmd_web.go` escribe la config de go2rtc. Lanza `Guard.Run`
con el contexto de la señal y apaga el servidor con `Shutdown` y 5 s de
gracia, de modo que `Guard.StopAll` se ejecuta antes de salir.

- [ ] **Paso 1: vendorizar htmx.** Descargar `https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js`
  en `internal/web/static/vendor/`. Comprobar su sha256 contra
  `https://cdn.jsdelivr.net/npm/htmx.org@2.0.4/dist/htmx.min.js` y apuntarlo
  en `internal/web/static/vendor/SOURCES.md`.
- [ ] **Paso 2: tests** (`server_test.go`, con un NVR falso de `httptest`,
  `Sessions` con clave fija y reloj falso):
  - `TestUnauthenticatedRedirects`: `GET /` → 303 `/login`.
    `GET /api/health` con `HX-Request` → 401 y `HX-Redirect`.
  - `TestLoginSuccessSetsCookie` y `TestLoginWrongPassword` (401, el
    formulario muestra "Usuario o contraseña incorrectos").
  - `TestLoginRateLimited`: el sexto intento → 429.
  - `TestCSRFRequired`: `POST /logout` con cookie y sin token → 403; con
    token → 303.
  - `TestPublicIPForbidden`: `RemoteAddr` `8.8.8.8:1234` → 403.
    `X-Forwarded-For: 8.8.8.8` desde `127.0.0.1` → 403. `X-Forwarded-For`
    desde una IP no confiable se ignora.
  - `TestSecurityHeaders`: el CSP está presente.
  - `TestSessionRefresh`: con `LastSeen` hace 2 h, la respuesta lleva un
    `Set-Cookie` nuevo.
  - `TestStaticServed`: `/static/vendor/htmx.min.js` → 200.
- [ ] **Paso 3:** `go test ./internal/web/...`. Resultado esperado: FAIL.
- [ ] **Paso 4: implementar** el servidor, el middleware, los handlers de
  login y health, `layout.html` (cabecera con el nombre, la barra de estado
  `hx-get="/api/health" hx-trigger="load, every 15s"` y el botón Salir) y
  `login.html`. `app.css` es un CSS propio y sobrio: modo oscuro por
  defecto, cuadrícula responsive de 2×2 en escritorio y 1 columna en móvil,
  y controles táctiles de 44 px como mínimo.
- [ ] **Paso 5:** `go test -race ./...`. Resultado esperado: PASS.
- [ ] **Paso 6:** commits
  - `feat(web): add server with security middleware and login`
  - `feat(cmd): add web and hash-password subcommands`

### Tarea 12: vista en directo (delegable a Grok/Kimi para la plantilla y el CSS; el JS de vídeo lo hace la sesión principal)

**Archivos:**
- Crear: `internal/web/handlers/{live,camera}.go`,
  `internal/web/templates/{live,camera}.html`, `internal/web/static/js/video.js`,
  `internal/web/static/vendor/{video-rtc.js,video-stream.js,LICENSE-go2rtc}`
- Test: añadir casos a `internal/web/server_test.go`

**Interfaces:**
- Consume: `camera.Service.Cameras`, `nvr.Client.Snapshot`, `Deps.Media`
- Produce: elementos HTML `<div class="tile" data-cam="1" data-quality="sub">`
  con `video.js`. El atributo `data-ws` vale
  `/api/stream/ws` y `video.js` elige el `src`.

`video.js` (módulo ES, sin dependencias salvo el `video-stream.js` de go2rtc):

```js
// Elige la variante de cada cámara según lo que decodifica el navegador
// y monta el componente <video-stream> de go2rtc.
import { VideoStream } from "/static/vendor/video-stream.js";

customElements.define("opennvr-video", class extends VideoStream {});

function supportsHEVC() {
  const mse = window.ManagedMediaSource || window.MediaSource;
  return !!(mse && mse.isTypeSupported('video/mp4; codecs="hvc1.1.6.L120.90"'));
}

export function streamName(cam, quality, hevc) {
  const base = `cam${cam}` + (quality === "sub" ? "_sub" : "");
  return hevc ? base : base + "_h264";
}

function mount(tile) {
  const hevc = supportsHEVC();
  const src = streamName(tile.dataset.cam, tile.dataset.quality, hevc);
  const url = new URL(`/api/stream/ws?src=${encodeURIComponent(src)}`, location.href);
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const v = document.createElement("opennvr-video");
  v.mode = "webrtc,mse";
  v.background = false;
  v.src = url.toString();
  if (tile.dataset.quality === "sub") v.muted = true;
  tile.querySelector(".video").replaceChildren(v);
}

document.querySelectorAll(".tile[data-cam]").forEach(mount);
```

Hay que comprobar en el código fuente de `video-stream.js` y
`video-rtc.js` de la versión fijada de go2rtc que existen las propiedades
`mode`, `background`, `src` y `muted`, y ajustar los nombres si no existen.
La versión vendorizada debe coincidir con la etiqueta de go2rtc del compose.
Los archivos de go2rtc (MIT) se copian desde
`https://raw.githubusercontent.com/AlexxIT/go2rtc/v<versión>/www/`. Su
sha256 y la versión van en `SOURCES.md`.

Plantillas:
- `live.html`: cuadrícula con las cámaras de `Cameras()`. En cada celda:
  nombre "Cámara N", un punto de estado (`Online` o `LoginStatus`), el
  vídeo `sub` silenciado y un enlace a `/camera/N`. Si `Cameras` falla, se
  muestra un aviso "No se pudo leer la lista de cámaras del NVR" y celdas
  para `CAMERA_CHANNELS`, porque el vídeo puede funcionar aunque la API no.
- `camera.html`: vídeo `main` con audio (con un botón "Activar sonido",
  porque los navegadores bloquean el autoplay con audio), el panel PTZ
  (Tarea 13) y el botón "Captura", que abre `/camera/N/snapshot` en una
  pestaña nueva.

- [ ] **Paso 1: tests**
  - `TestLiveListsCameras`: las 4 celdas y `data-cam="1"` hasta `"4"`.
  - `TestLiveNVRDown`: con el NVR falso cerrado, 200, el aviso y 4 celdas.
  - `TestCameraInvalidChannel`: `/camera/9` y `/camera/x` → 404.
  - `TestSnapshot`: `Content-Type: image/jpeg` y `Cache-Control: no-store`.
  - `TestStreamProxyRequiresAuth` y `TestStreamProxyChecksOrigin`: un
    `Origin` de otro host → 403.
- [ ] **Paso 2:** FAIL, implementar, PASS (`go test -race ./internal/web/...`).
- [ ] **Paso 3: prueba local en el navegador sin el NVR real.** Arrancar
  `NVR_HOST=127.0.0.1 NVR_HTTP_PORT=1 WEB_LISTEN=127.0.0.1:8095 WEB_PASSWORD_HASH=... WEB_SESSION_KEY=... GO2RTC_CONFIG=$SP/go2rtc.yaml go run ./cmd/opennvr web`
  (el NVR inexistente ejercita el aviso de error) y comprobar con
  Playwright que el login y la cuadrícula se pintan y que la consola no
  muestra errores de CSP. El vídeo real se prueba en la Tarea 15.
- [ ] **Paso 4:** commits
  - `feat(web): add live grid and camera view`
  - `chore(web): vendor go2rtc video player`

### Tarea 13: controles PTZ en la UI (sesión principal)

**Archivos:**
- Crear: `internal/web/handlers/ptz.go`, `internal/web/static/js/ptz.js`
- Modificar: `internal/web/templates/camera.html`
- Test: añadir casos a `server_test.go`

**Interfaces:**
- Consume: `camera.Guard` (Start, Keepalive, Stop), `nvr.ValidPTZCommand`,
  `nvr.Client.{Preset,Light,Wiper}`
- Produce: handlers JSON y parciales mínimos:
  - `POST /api/ptz/{ch}/start`, formulario `cmd=TurnLeft&speed=5` → 204.
    Un `cmd` no válido → 400.
  - `POST /api/ptz/{ch}/keepalive` → 204, o 409 si no hay movimiento
    activo, y entonces el JS deja de enviar.
  - `POST /api/ptz/{ch}/stop` → 204, siempre idempotente.
  - `POST /api/ptz/{ch}/preset/{goto|set|remove}/{idx}` (`idx` de 1 a 256 en
    la URL; se envía `idx-1` al NVR) → parcial `<span class="ok">` o
    `<span class="error">`.
  - `POST /api/ptz/{ch}/{light|wiper}/{on|off}` → parcial.
  - Errores del NVR: `ErrUnavailable` → 503 "El NVR no responde",
    `ErrUnauthorized` → 502 "El NVR rechazó las credenciales",
    `BadResponseError` → 502 "El NVR devolvió statusCode N".

`ptz.js`:

```js
// Movimiento continuo con stop de seguridad: start al pulsar, keepalive
// cada 500 ms mientras se mantiene, stop al soltar, cancelar, perder el
// foco u ocultar la página. El servidor para solo si dejan de llegar
// keepalives (1,5 s).
const csrf = JSON.parse(document.body.getAttribute("hx-headers"))["X-CSRF-Token"];

function post(url, data) {
  return fetch(url, {
    method: "POST",
    headers: { "X-CSRF-Token": csrf, "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(data || {}),
    keepalive: true,
  });
}

let active = null; // {ch, timer}

function stop() {
  if (!active) return;
  clearInterval(active.timer);
  const ch = active.ch;
  active = null;
  post(`/api/ptz/${ch}/stop`);
}

function start(ch, cmd) {
  stop();
  const speed = document.querySelector("#ptz-speed").value;
  post(`/api/ptz/${ch}/start`, { cmd, speed });
  active = {
    ch,
    timer: setInterval(async () => {
      const r = await post(`/api/ptz/${ch}/keepalive`);
      if (r.status === 409) stop();
    }, 500),
  };
}

document.querySelectorAll("[data-ptz]").forEach((btn) => {
  btn.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    btn.setPointerCapture(e.pointerId);
    start(btn.dataset.ch, btn.dataset.ptz);
  });
  btn.addEventListener("pointerup", stop);
  btn.addEventListener("pointercancel", stop);
  btn.addEventListener("lostpointercapture", stop);
  btn.addEventListener("contextmenu", (e) => e.preventDefault());
});
window.addEventListener("blur", stop);
document.addEventListener("visibilitychange", () => { if (document.hidden) stop(); });
window.addEventListener("pagehide", stop);
```

En `camera.html` va un pad de 3×3 con las 8 direcciones (`data-ptz="TurnUpLeft"`,
etc.), un centro vacío, zoom ±, enfoque ±, un deslizador de velocidad de 1 a
10 (por defecto 5), presets (número de 1 a 256 con botones Ir, Guardar y
Borrar; Borrar usa `hx-confirm`), luz encendida/apagada y limpiaparabrisas
encendido/apagado. Los presets, la luz y el limpiaparabrisas usan htmx con
`hx-post` y `hx-target` en un `<output>`.

- [ ] **Paso 1: tests** (`fakeMover` o el NVR falso, que registra las rutas
  recibidas):
  - `TestPTZStartKeepaliveStop`: la secuencia produce `PUT /PTZ/1/TurnLeft`
    con `Param1=1` y, al parar, con `Param1=0`.
  - `TestPTZInvalidCommand` → 400, sin llamadas al NVR.
  - `TestPTZKeepaliveWithoutStart` → 409.
  - `TestPTZRequiresCSRF` → 403.
  - `TestPresetIndexTranslation`: `/preset/goto/3` → `Param1=2`.
    `/preset/goto/0` y `/preset/goto/257` → 400.
  - `TestLightOff`: `PUT /PTZ/2/Light` con `Param1=0`.
  - `TestPTZNVRDown` → 503 con el mensaje.
- [ ] **Paso 2:** FAIL, implementar, PASS.
- [ ] **Paso 3:** prueba en el navegador con el NVR falso. En la pestaña de
  red de Playwright, al mantener pulsado 2 s hay que ver un start y unos 4
  keepalive; al soltar, un stop. Al cambiar de pestaña con el botón
  pulsado, un stop.
- [ ] **Paso 4:** commit `feat(web): add PTZ controls with keepalive-based safety stop`.

### Tarea 14: despliegue de la fase 2 (sesión principal + propietario)

**Archivos:**
- Modificar: `deploy/baphomet/compose.yaml`, `deploy/baphomet/.env.example`,
  `deploy/baphomet/nftables/opennvr.nft`, `deploy/baphomet/README.md`,
  `.github/workflows/images.yml`
- Crear: `deploy/baphomet/caddy/Dockerfile`, `deploy/baphomet/caddy/nvr.Caddyfile`

- [ ] **Paso 1: compose.** Se añade lo siguiente. `web` y `go2rtc` usan la
  red del host: go2rtc necesita anunciar 192.168.0.8 como candidato
  WebRTC, y así la API de go2rtc y la app web escuchan solo en loopback o
  en una IP concreta.

```yaml
  web:
    container_name: opennvr-web
    image: ghcr.io/belcaik/open-nvr:${OPENNVR_TAG:-latest}
    command: ["web"]
    network_mode: host
    env_file: .env
    environment:
      NVR_PASS_FILE: /run/secrets/nvr_pass
      WEB_PASSWORD_HASH_FILE: /run/secrets/web_password_hash
      WEB_SESSION_KEY_FILE: /run/secrets/web_session_key
      GO2RTC_CONFIG: /config/go2rtc.yaml
    secrets: [nvr_pass, web_password_hash, web_session_key]
    volumes:
      - go2rtc_config:/config
    restart: unless-stopped
    read_only: true
    tmpfs: ["/tmp:size=16m,mode=1777"]
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    healthcheck:
      test: ["CMD-SHELL", "/opennvr healthcheck --url http://$${WEB_LISTEN:-127.0.0.1:8095}/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s

  go2rtc:
    container_name: opennvr-go2rtc
    image: docker.io/alexxit/go2rtc:${GO2RTC_TAG:-1.9.9-hardware}
    network_mode: host
    command: ["go2rtc", "-config", "/config/go2rtc.yaml"]
    devices: ["/dev/dri/renderD128:/dev/dri/renderD128"]
    group_add: ["keep-groups"]
    volumes:
      - go2rtc_config:/config:ro
    depends_on:
      web:
        condition: service_healthy
    restart: unless-stopped
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
```

  Hay que comprobar que la etiqueta existe
  (`docker manifest inspect docker.io/alexxit/go2rtc:1.9.9-hardware`), fijar
  la más reciente `1.9.x-hardware` que exista y vendorizar el `video-rtc.js`
  de esa misma versión (Tarea 12). `keep-groups` es de podman con crun; si
  falla en rootful, se sustituye por el GID de `render` en el host
  (`getent group render`). Añadir `go2rtc_config:` a `volumes:` y
  `web_password_hash` y `web_session_key` como secretos `external`.

- [ ] **Paso 2: nft.** En `chain input`, antes del contador del NVR:

```
		# Interfaz web: solo el propio host (Caddy). Ver deploy/baphomet/README.md.
		tcp dport 8095 fib saddr type local accept
		tcp dport 8095 counter drop
		# Medios WebRTC de go2rtc: solo la LAN.
		meta l4proto { tcp, udp } th dport 8555 ip saddr != 192.168.0.0/24 counter drop
```

- [ ] **Paso 3: Caddy.**

  `deploy/baphomet/caddy/Dockerfile`:

```dockerfile
FROM docker.io/library/caddy:2.10.0-builder AS build
RUN xcaddy build --with github.com/caddy-dns/cloudflare
FROM docker.io/library/caddy:2.10.0
COPY --from=build /usr/bin/caddy /usr/bin/caddy
```

  Las etiquetas se verifican con `docker manifest inspect` y se fija la
  versión 2.x más reciente que exista. Para fijar el módulo:
  `--with github.com/caddy-dns/cloudflare@<último tag>`.

  `deploy/baphomet/caddy/nvr.Caddyfile` (bloque para añadir al Caddyfile
  existente; requiere `CF_API_TOKEN` en el entorno del contenedor Caddy):

```
nvr.belcaik.com {
	tls {
		dns cloudflare {env.CF_API_TOKEN}
	}
	@public not remote_ip private_ranges
	respond @public 403
	reverse_proxy 192.168.0.8:8095
}
```

  `WEB_LISTEN` queda así en `.env.example`: Caddy es un contenedor sin
  root en modo bridge y no puede alcanzar el loopback del host, así que se
  usa `WEB_LISTEN=192.168.0.8:8095` y el acceso se protege con la regla
  `fib saddr type local` del paso 2. Esto es lo que la spec preveía como
  alternativa. Hay que verificar en el paso 6 que Caddy llega a la app y
  que desde otra máquina de la LAN `curl http://192.168.0.8:8095/healthz`
  falla. Si Caddy aparece con una IP de origen no local (por ejemplo, la
  de slirp4netns), se cambia la regla a esa IP concreta y se documenta.

  CI: en la matriz `images`, una entrada `caddy` con contexto
  `deploy/baphomet/caddy` → `ghcr.io/belcaik/open-nvr-caddy`.

- [ ] **Paso 4: README de despliegue.** Nueva sección "Interfaz web" con:
  1. Los secretos:

```bash
podman run --rm -i ghcr.io/belcaik/open-nvr:latest hash-password | sudo podman secret create web_password_hash -
podman run --rm ghcr.io/belcaik/open-nvr:latest hash-password --gen-key | sudo podman secret create web_session_key -
```

  2. El registro DNS en Cloudflare: `A nvr.belcaik.com 192.168.0.8`, sin
     proxy (nube gris).
  3. El token de Cloudflare con permiso `Zone.DNS:Edit` solo para
     `belcaik.com`, que se pasa al Caddy existente (rootless, en
     `/opt/services/caddy`) como `CF_API_TOKEN`, cambiando su imagen a
     `ghcr.io/belcaik/open-nvr-caddy:latest`.
  4. Copiar el bloque `nvr.Caddyfile` al Caddyfile y ejecutar
     `podman exec caddy caddy reload --config /etc/caddy/Caddyfile`.
  5. `sudo systemctl restart opennvr-nft` (reglas nuevas) y
     `sudo podman-compose up -d web go2rtc`.
  6. Reversión: `sudo podman-compose stop web go2rtc` y restaurar la imagen
     anterior de Caddy.
  7. Para rotar sesiones: recrear `web_session_key` y reiniciar `web`.

- [ ] **Paso 5:** dry run de podman-compose 1.0.6 (el comando de la
  Tarea 5) para `web` y `go2rtc`: el healthcheck, `--device`,
  `--network host` y `--group-add` deben salir bien. Ejecutar
  `nft -c -f deploy/baphomet/nftables/opennvr.nft` si hay `nft` local; si
  no, se comprueba en Baphomet.
- [ ] **Paso 6 (propietario y verificación):** ejecutar el README. Luego,
  desde la sesión:
  - `curl -sk https://nvr.belcaik.com/healthz --resolve nvr.belcaik.com:443:192.168.0.8`
    → `ok`, con un certificado de Let's Encrypt válido (`curl -v`, que
    muestra el emisor).
  - `curl -s -m 3 http://192.168.0.8:8095/healthz` desde este portátil →
    timeout o rechazo.
  - `ssh 192.168.0.8 'ss -tlnp | grep -E "1984|8095|8555"'`: `1984` solo
    en `127.0.0.1`.
  - En el host, `sudo podman logs opennvr-go2rtc` sin errores de RTSP.
- [ ] **Paso 7:** commits
  - `feat(deploy): add web and go2rtc services`
  - `feat(deploy): restrict web and WebRTC ports in nftables`
  - `build(caddy): add Caddy image with Cloudflare DNS module`
  - `docs(deploy): document web interface setup`

### Tarea 15: verificación en navegadores y cierre de las fases 1 y 2

- [ ] **Paso 1:** probar desde `https://nvr.belcaik.com` (lo hace el
  propietario en Android; la sesión usa Playwright con Chromium y Firefox
  desde el portátil):

| Prueba | Chrome/Edge | Firefox | Android |
|---|---|---|---|
| Login y cookie persistente tras `podman restart opennvr-web` | | | |
| Cuadrícula con 4 cámaras en movimiento | | | |
| Vista de una cámara con audio | | | |
| Fuente usada (HEVC o `_h264`) según la pestaña de red | | | |
| PTZ con las 8 direcciones, zoom y enfoque | | | |
| Stop de seguridad: mantener pulsado y cerrar la pestaña → la cámara para en menos de 2 s | | | |
| Presets: ir y guardar | | | |
| Luz encendida/apagada y limpiaparabrisas en el canal 1; probar también en el canal 2 (¿solo vale el canal 1?) | | | |
| Captura JPEG | | | |
| CPU de Baphomet con Firefox viendo `_h264` (`top`; `radeontop` si está instalado) | | | |

- [ ] **Paso 2:** registrar las filas como T71–T7x en `docs/test-log.md`,
  con su evidencia (capturas en `captures/web/`, que es un directorio
  ignorado).
- [ ] **Paso 3:** en `README.md`, actualizar la tabla de estado
  (interfaz web ✅, grabación ✅ porque ya está resuelta, cámaras 2–4 ✅) y
  la sección de arquitectura.
- [ ] **Paso 4:** commits `docs(tests): verify web interface in browsers` y
  `docs: update status after web interface`.

---

## Reparto con `/cli-agent-orchestration`

| Tarea | Ejecutor | Motivo |
|---|---|---|
| 1, 2 | sesión principal | base de la que dependen todas las demás; es corta |
| 3 | Codex (worktree `go-nvrwatch`) | port mecánico con tests de referencia |
| 4 | Codex (worktree `go-egresswatch`), en paralelo con la 3 | ídem e independiente de la 3 |
| 5, 6 | sesión principal | toca despliegue y CI; la 6 la ejecuta el propietario |
| 7 | Kimi (worktree `go-nvr-client`), en paralelo con las 3 y 4 | interfaz cerrada y fixtures saneados |
| 8, 9, 10, 11 | sesión principal | seguridad y núcleo |
| 12 (plantillas y CSS) | Grok o Kimi, con la plantilla de datos fijada | HTML y CSS acotados |
| 12 (video.js), 13, 14, 15 | sesión principal | integración con el equipo real |

Cada agente recibe el texto completo de su tarea, las Restricciones globales
y la spec. No recibe credenciales ni la ruta `~/.config/open-nvr/env`, y
trabaja en un worktree. Su resultado se revisa (tests más lectura del diff)
antes de integrarlo con merge o cherry-pick en `main`.
