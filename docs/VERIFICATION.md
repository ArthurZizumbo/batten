# Verificación — batten v2

Resultados de la verificación end-to-end (P4). Todo probado con el binario real y hooks
simulados por inyección de JSON, salvo lo marcado como **pendiente E0** (requiere el plugin
instalado en Claude Code — solo Arthur puede correrlo).

## Suite

- `go build ./...` · `go vet ./...` · `go test ./...` — **limpios, todos verdes**.

## P1 — instalación / rampa

| Prueba | Resultado |
|---|---|
| `batten init` sobre repo TS | ✓ derivó `PROJ-\d{2}` de la rama, dominio `src/` con `pnpm lint/typecheck/test` desde package.json, detectó engram |
| Borrador válido | ✓ `doctor` lo carga; TODOs honestos donde falta juicio |
| `enforcement: report` | ✓ commit sin veredicto → **WARN** (no bloquea) |
| flip a `enforce` | ✓ mismo commit → **DENY** |

## P2 — integraciones

| Prueba | Resultado |
|---|---|
| Vault automático tras veredicto | ✓ nota + canvas + 3 dashboards `.base` sin `batten canvas` manual |
| engram en commands | ✓ `mem_search`/`mem_save` reinyectados (plan/verify/close) |
| graphify staleness | ✓ doctor avisa si `graph.json` < HEAD |
| headroom `measure` | ✓ con 2 runs dice "insufficient — need ≥3"; no inventa conclusión |

## P2.5 — multi-sesión

| Prueba | Resultado |
|---|---|
| Binding sesión↔run | ✓ US-034→sessA, US-051→sessB vía hook PostToolUse |
| Guard entre runs | ✓ sessB escribiendo archivo de US-034 → **DENY** nombrando la unidad en conflicto |
| Ambigüedad visible | ✓ sessionStart avisa cuando la sesión no está ligada |

## P3 — distribución

| Prueba | Resultado |
|---|---|
| `.goreleaser.yaml`, `release.yml` | ✓ YAML válido |
| `bootstrap.sh`, `build-plugin.{sh,ps1}` | ✓ sintaxis OK |
| hooks.json con bootstrap + PostToolUse | ✓ JSON válido |

## P4 — regresión / migración

| Prueba | Resultado |
|---|---|
| Sin regresión en gates/guard/canvas/contabilidad | ✓ |
| Migración user_version 0→1 | ✓ DB estilo MVP (sin `headroom`) abre y migra en sitio, sin rebuild |
| Validador atrapa specs malos | ✓ atrapó `HANDOFF.md` sin `{id}` en el propio dogfood yaml |

## Pendiente E0 (solo Arthur, con el plugin instalado)

Ver `docs/E0-DOGFOOD.md`. Los dos que pueden cambiar diseño:

1. **`.exe` en hooks exec-form en Windows** — si `${CLAUDE_PLUGIN_ROOT}/bin/batten` no resuelve,
   cambiar a `.exe` o shell-form.
2. **`agent_id` en `PreToolUse` de subagente** — capturar con `batten hook-debug --tap` →
   `--show`. Si no llega, el guard cae a modo advisory (ya soportado por `enforcement`).

Los otros tres (MCP con cliente real, TUI interactiva, statusline en terminal real) son
confirmaciones, no riesgos de diseño.

---

## v3 — robustez pre-pruebas (R1-R4 + M1)

| Prueba | Resultado |
|---|---|
| R1 auto-close: commit pasa gate → run cierra → otro run reclama el archivo liberado | ✓ |
| R1 `batten close --status ok` sin veredicto | ✓ rechazado |
| R1 doctor avisa run open >48h sin eventos | ✓ |
| R2 `batten check` corre checks reales; uno falla → blocked citando el error de git | ✓ |
| R2 commit con checks declarados exige veredicto source='batten' | ✓ agent-only "ok" → DENY "run batten check" |
| R3 payload JSON malformado / tipos incorrectos a hook | ✓ exit 0, sin stack trace |
| R4 escribir `ML/F.PY` contra claim de `ml/f.py` en Windows | ✓ DENY (case-fold) |
| M1 `batten show` marca nodo con modelo real ≠ declarado | ✓ "declared opus" cuando corrió haiku |
| M1 `batten measure` desglosa spend por modelo | ✓ |
| M1 validación: `models.phases` con fase inexistente | ✓ spec inválido |
| Migración user_version 1→2 (columna verdicts.source) | ✓ en sitio |

Suite completa (`go build/vet/test ./...`) verde tras cada bloque.
