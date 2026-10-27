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
