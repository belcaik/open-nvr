# Validación de la orquestación de agentes (2026-10-26)

Registro de la skill `cli-agent-orchestration` usada para preparar la
documentación. Es una herramienta de desarrollo y no forma parte del proyecto.
Ningún agente tuvo acceso al NVR ni a la red local: solo trabajaron en
directorios temporales aislados y con búsqueda web.

## Versiones detectadas (`orchestrate.py discover`)

| Agente | Versión |
|---|---|
| Claude Code | 2.1.304 |
| Codex | codex-cli 0.163.0 |
| Kimi Code | 2.3.3 |
| Grok | 1.0.70 |

## Procesos dobles

| Run | Comando | Estado | Duración |
|---|---|---|---|
| test-pass | `echo ok` | passed (exit 0) | 0,0 s |
| test-fail | `sh -c 'echo bad >&2; exit 3'` | failed (exit 3) | 0,0 s |
| test-timeout | `sleep 30` con timeout 2 s | timeout (exit -9) | 2,0 s |
| test-cancel | `sleep 60` en segundo plano + `cancel` | cancelled (exit -15) | 2,9 s |

## Smoke por agente (lanzados en paralelo)

Tarea: crear `smoke.txt` con `ok-<agente>` en un workspace propio.

| Agente | Comando (sanitizado) | Estado | Duración | Artefacto |
|---|---|---|---|---|
| claude | `claude -p '…' --output-format json --permission-mode acceptEdits --allowedTools '…'` | passed | 5,8 s | `ok-claude` |
| codex | `codex exec --sandbox workspace-write -C <ws> --json '…'` | failed (exit 1) | 0,1 s | — |
| codex (reintento) | igual, con `<ws>` inicializado como repo git | passed | 9,9 s | `ok-codex` |
| kimi | `kimi -p '…' --output-format stream-json` | passed | 9,1 s | `ok-kimi` |
| grok | `grok -p '…' --output-format json --permission-mode acceptEdits --cwd <ws>` | passed | 21,5 s | `ok-grok` |

Codex falló con `Not inside a trusted directory and --skip-git-repo-check was
not specified.` Su workspace tiene que ser un repo git. Hay dos soluciones:
inicializar git en el workspace (la que se usó aquí) o que `orchestrate.py`
añada `--skip-git-repo-check` cuando no se use `--worktree`.

## Trabajo real delegado

| Run | Agente | Objetivo | Estado | Duración |
|---|---|---|---|---|
| research-vendor | grok | Documentación pública del fabricante, OEM, ONVIF | passed | 603 s |
| research-linux | kimi | ONVIF (PTZ, Imaging, backchannel) y herramientas Linux | passed | 473 s |

Las dos investigaciones se ejecutaron en paralelo. Claude revisó los
resultados y los incorporó a [protocols.md](protocols.md). Ningún agente
escribió en el árbol del repositorio.

## Conclusiones

- Los cuatro agentes están instalados, autenticados y funcionan sin flags de
  bypass.
- `orchestrate.py` no escribe `assignments.jsonl`; las asignaciones se
  registraron a mano en `~/.agents/orchestration/assignments.jsonl`.

## Etapas 1–4 (2026-10-26/27)

| Run | Agente | Objetivo | Estado | Duración |
|---|---|---|---|---|
| api-analysis | codex | Análisis estático de la UI web → `docs/api-reference.md` | passed | 346 s |
| research-security | kimi | Contexto público de seguridad de los componentes | passed | 317 s |
| stage4-baphomet | claude | Implementar el monitoreo de Baphomet | failed (exit 1): se agotó el límite de sesión de la suscripción con el trabajo a medias | 1022 s |
| stage4-finish | codex | Terminar la etapa 4: 4 tests corregidos, compose, `.env.example`, README, `monitoring.md` | passed | 355 s |

El sondeo activo del NVR (etapas 1 y 2) no se delegó: necesitaba las
credenciales y peticiones de una en una. Ningún agente tuvo acceso a la red
local ni a las credenciales; trabajaron sobre copias aisladas sin secretos.
Validación final en el repositorio: 58 tests unitarios pasan y `py_compile`
termina sin errores.

## Investigación EasyVideo con REA (2026-10-28)

Se repitió `discover`: Claude 2.1.304, Codex 0.163.0, Kimi **2.4.0** y Grok
1.0.70. Como Kimi había cambiado de versión, se repitió su smoke en aislamiento:
`smoke-kimi-2.4.0`, **passed**, 28,48 s, artefacto `ok-kimi-2.4.0`.

| Run | Agente | Objetivo | Estado | Duración |
|---|---|---|---|---|
| rea-login-talk | codex | Login P6S y audio bidireccional | passed | 613,87 s |
| rea-ipc-catalog | claude | Configuración y catálogo IPC | failed: rechazo de salvaguarda del agente | — |
| rea-ipc-catalog-codex | codex | Configuración, contraseñas IPC, catálogo y phone-home | passed | 445,54 s |
| web-gap | grok | Brechas entre la UI original y open-nvr | passed | 337,59 s |
| rea-playback | kimi | Reproducción y descarga | cancelled: bucle de `close_binary` con snapshot tras un error no recuperable | ~52 min |
| rea-playback-recovery | kimi | Reproducción y descarga con recuperación acotada | passed | 916,44 s |

Los informes quedaron en `~/.agents/orchestration/ws/<run>/out/REPORT.md`.
`rea-playback-recovery` exportó 17 registros REA en `out/evidence-bundle.json`.
No se ejecutaron PE, no se usó Wine y ningún agente abrió conexiones de red.
Todos escribieron en workspaces separados. La fórmula de login pasó los vectores
de Python y una segunda implementación independiente con `crypto/des` de Go.
Los resultados estáticos se incorporaron a `p6s-protocol.md`, `api-reference.md`
y `security.md`; las incógnitas de framing y validación en el NVR permanecen
marcadas como tales.

## Implementación offline P6S (2026-10-28)

Sin Claude, por petición del propietario. Se repitió `discover` antes de delegar.
El modo `--worktree` del helper apuntó incorrectamente a `~/.agents`; no inició
agentes. Se crearon los worktrees desde el repo y se usó `--workspace`.

| Run | Agente | Entregable | Estado | Duración |
|---|---|---|---|---|
| p6s-core-impl | codex | LoginFlag y framing comandos/medios | passed | 214,61 s |
| p6s-history-impl | kimi | XML HistoryStream/FileDownload | passed | 430,49 s |
| p6s-history-fix | kimi | PosTime, Version y Speed tras revisión | passed | 384,74 s |
| p6s-voice-impl | grok | PCMU, VoiceStream y extensión opaca | passed | 721,41 s |
| p6s-login-session | codex | Login net.Conn, heartbeat y cancelación | passed | 229,80 s |

La integración fue manual, sin merges automáticos. Se añadieron pruebas cruzadas
y se verificó en la captura que el NUL final forma parte del XML de voz. Estado
final: `go test -race ./...`, `go vet ./...` y `git diff --check` pasan. No se
contactó con el NVR porque las credenciales no estaban en el entorno.

## Validación real P6S y reproducción (2026-10-28)

Se cargaron las credenciales desde el entorno sin imprimirlas. El primer probe
encontró XML de firmware con una capacidad sin nombre; se añadió una regresión y
un filtro exclusivo para ese patrón. Después, `UserLogin` respondió `CmdReply=22`
aunque `/System/DeviceInfo` con las mismas credenciales devolvió HTTP 200.

| Run | Agente | Entregable | Estado | Duración |
|---|---|---|---|---|
| p6s-malformed-login | codex | No inició: workspace no Git confiable | failed | 0,11 s |
| p6s-login-research | codex | CmdReply 22 y primera lectura de DES/XML | passed | 357,26 s |
| p6s-login-crosscheck | kimi | Aleatorio, bitreverse y vectores independientes | passed | 740,71 s |
| p6s-login-key-path | kimi | Cadena de credencial; sin informe | timeout | 900,05 s |
| p6s-login-wrapper | grok | Wrapper no transforma password; log en claro | passed | 437,70 s |
| p6s-login-caller | grok | Caller; sin salida | timeout | 600,03 s |

El análisis directo posterior identificó la cadena `EasyVideo.exe` → `EvPlugs.dll`
→ `VNetCIU.dll` → `VideoNetClient_UserLogin`. No usa `SuperUserLogin` ni transforma
la contraseña. La comprobación en la UI confirmó `config.password === NVR_PASS`.
Aun así, los vectores estáticos no reproducen el ciphertext oficial y el probe Go
sigue recibiendo 22. Esto queda bloqueado y documentado, sin más cambios a ciegas.

En una VM aislada se instaló el cliente oficial, se comprobó el mismo SHA-256 del
DLL y se capturó una reproducción real completa. Evidencia:
`win10-p6s-login-current.pcap`, `win10-p6s-login-fresh.pcap`,
`win10-p6s-playback-search.pcap`, `win10-p6s-playback-close.pcap`.
Se recuperó `HistoryStreamCreateMultiType` (185), los valores reales de sesión,
la extensión de vídeo de 44 bytes, MPEG-PS y el cierre 130/126. La VM quedó
suspendida y sin captura activa. `go test -race ./...`, `go vet ./...` y
`git diff --check` vuelven a pasar tras la instrumentación y su limpieza.

## Cierre del login y catálogo funcional (2026-10-29)

Se repitió `discover`: Claude 2.1.304, Codex 0.163.0, Kimi 2.4.0 y Grok 1.0.70.
No se usó Claude. Los workspaces quedaron aislados y sin credenciales; la VM y
el NVR sólo se usaron desde el proceso principal.

| Run | Agente | Objetivo | Estado | Duración |
|---|---|---|---|---|
| dvr-feature-inventory | grok | Catálogo trazable HTTP/P6S y brechas de integración | passed | 550,84 s |
| p6s-des-audit | codex | Revisión independiente de DES y S-box | passed | 234,45 s |
| p6s-des-validate | codex | Validación diferencial contra los dos pcaps oficiales | passed | 351,92 s |

La instrumentación Frida en la VM confirmó que wrapper, `CUser::Login` y
`CTripleDES::Encode` reciben la misma contraseña y recuperó el plaintext real
(evidence `ev_31d5fbf1236219448f066b37ccc518be9af65882f4e4df0d5379b52bc017b862`).
Las dos diferencias necesarias fueron salida S-box LSB-first y el intercambio de
bits adyacentes antes del hexadecimal. Los dos pcaps oficiales quedaron con
`reference_match=true` y el probe Go real completó el login con `CmdReply=0`.
`go test -race ./...`, `go vet ./...` y `git diff --check` pasan.

## Integración P6S: histórico, MPEG-PS y voz (2026-10-29)

Se repitió `discover`: Claude 2.1.304, Codex 0.163.0, Kimi 2.4.0 y Grok 1.0.70.
No se usó Claude. Cada agente trabajó en un worktree propio, sin credenciales ni
red; la integración y las pruebas contra el DVR fueron del proceso principal.

| Run | Agente | Objetivo | Estado | Duración |
|---|---|---|---|---|
| p6s-history-multitype | codex | `MultiType`, seek y sesión histórica | passed | 409,62 s |
| p6s-mpegps-parser | kimi | Parser MPEG-PS y fixtures | timeout; código revisado y límites completados por el proceso principal | 1200,05 s |
| p6s-voice-session | grok | Sesión de voz, fixture PCMU y cierre | passed | 1140,54 s |
| p6s-aux-research | codex | Contratos estáticos y brechas auxiliares | passed | 380,58 s |

Los tres paquetes se integraron manualmente. La revisión independiente detectó
carreras en envíos de voz, timestamps PES que podían cruzar al paquete siguiente
y un cierre de errores incompleto del probe; se añadieron regresiones y se
corrigieron antes de repetir las pruebas reales. Voz: tres frames de silencio y
stop/destroy correctos. Histórico: MultiType/seek/cierre correctos y clip
`elemental-b7` de 4.172.197 bytes. El parser MPEG-PS queda probado con fixtures,
no validado por ese clip. Descarga y auxiliares permanecen sin evidencia runtime
suficiente. Validación final: `go test -race ./...`, `go vet ./...` y
`git diff --check` pasan.
