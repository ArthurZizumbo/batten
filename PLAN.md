# batten v3 — robustez pre-pruebas + ruteo de modelos

## Contexto

El plan v2 se ejecutó completo (7 commits en `feat/batten-mvp`). Antes del dogfooding E0, la revisión adversarial encontró 4 huecos de robustez —dos críticos verificados con evidencia— y Arthur pidió una capacidad nueva: **rutear modelos de Anthropic por dificultad de tarea** (planear merece un modelo grande; cambiar un color, no).

Evidencia de los huecos: `grep CloseRun` → solo un test lo llama (nadie cierra runs jamás); `grep recover()` en cmd/main + hooks → 0 (sin panic fence en la superficie que más importa).

---

## R1 — Ciclo de vida de runs: nada los cierra (CRÍTICO)

Hoy cada `batten phase` abre un run que queda `running` para siempre → los write-set claims **nunca se liberan** (el guard cruzado filtra `status='running'`, así que cerrar libera naturalmente), los runs zombis acumulan ambigüedad y las sesiones nuevas dejan de auto-ligarse.

- **`batten close <unit> [--status ok|failed|rolled_back]`** (default ok). Cerrar como `ok` exige veredicto ok u override —la misma regla del commit gate, coherencia total. `failed`/`rolled_back` cierran libre. Al cerrar: `CloseRun` + refresh del vault (`export.Run`) + mensaje "write-set claims released".
- **Auto-close**: en `postToolUse`, si el comando fue un `git commit` que pasó el gate y la fase actual es la de cierre (`requires_verdict`), cerrar el run como `ok` (best-effort; si `tool_response` trae error, no cerrar).
- **`doctor`**: avisar de runs `running` con >48h sin eventos ("stale — ciérralo con batten close o retómalo").

## R2 — `batten check`: evidencia generada, no afirmada (CRÍTICO)

Hoy el agente puede escribir `"pytest: 142 passed"` sin haber corrido pytest — el gate mata el "se ve bien" pero no el "inventé la evidencia". El spec ya declara `gates.<g>.checks`: batten sabe qué comandos son la verdad.

- **`batten check <unit> [--gate <g>]`**: corre cada comando de `checks` (shell del OS: `cmd /C` en Windows, `sh -c` si no; timeout 5 min c/u), captura exit code + cola del output (~2KB). Todos pasan → veredicto `ok` con evidencia `"<cmd>: PASS (exit 0)"`; alguno falla → `blocked` citando la cola real del fallo.
- **Migración v2 del store** (`user_version` 1→2): columna `verdicts.source TEXT DEFAULT 'agent'` — `batten check` graba con `source='batten'`.
- **Endurecer el commit gate**: si el gate declara `checks`, exigir ADEMÁS que el último veredicto `source='batten'` sea `ok` (mensaje de deny: "run batten check"). El veredicto del agente sigue existiendo para el juicio (criterios de aceptación); la parte mecánica deja de ser confiable-porque-lo-dice-el-modelo y pasa a confiable-por-construcción.
- `batten-verify.md`: instruir "corre `batten check` PRIMERO, añade tu sobre de juicio después".

## R3 — Panic fence en `cmdHook` (alto, barato)

El statusline tiene `recover()`; los hooks no — un panic pintaría un stack trace de Go en la sesión. `defer recover()` en `cmdHook` → salir 0 en silencio (mismo patrón que statusline.Run). Principio #2: degradar, jamás romper.

## R4 — Case-fold de paths en Windows (medio, barato)

`ml/F.py` vs `ml/f.py`: mismo archivo en Windows, dos entradas para el guard → la cerca se cruza variando el casing. Fix: `store.normPath` + la normalización en `writeSetGuard` hacen `strings.ToLower` cuando `runtime.GOOS=="windows"`.

## M1 — Ruteo de modelos por dificultad (la petición nueva)

**Mecanismo real disponible**: batten no puede cambiar el modelo del loop principal (eso es el `/model` del usuario — limitación honesta, va documentada), pero los **subagentes sí aceptan modelo**: el Agent tool tiene parámetro `model`, y los custom agents (`domains[].agent`) llevan `model` en su frontmatter. Y batten tiene lo que nadie: el ledger **ya registra qué modelo usó realmente cada nodo** (`usage.model` del transcript) → puede declarar el ruteo Y verificar que se cumplió.

**Spec** (`internal/spec/spec.go` + `batten.schema.json` + ejemplo agrosat):
```yaml
models:
  tiers:                 # el planeador clasifica cada sub-tarea en un tier
    mechanical: haiku    # renombres, colores, strings i18n, config
    moderate: sonnet     # features normales
    complex: opus        # arquitectura, debugging duro, ML
  phases:                # fases con juicio pesado piden modelo fuerte
    plan: opus
    verify: opus
domains:
  frontend:
    model: haiku         # override por dominio (opcional, gana sobre el tier)
```
Validación: los `phases` del map deben referir fases existentes.

**Commands** (texto):
- `batten-plan.md`: clasificar cada sub-tarea (mechanical/moderate/complex) con 3 criterios simples (¿toca >2 archivos? ¿decisión de diseño? ¿puede fallar sutilmente?) y anotar tier+modelo junto a cada write-set en el artefacto del plan.
- `batten-build.md`: lanzar cada subagente con su modelo asignado (param `model` del Agent tool; `domains[].model` > tier del plan > default de sesión).

**Verificación del ruteo (el twist batten)**:
- `store`: `ModelsByNode(runID)` (modelo(s) reales por nodo, del ledger) y `MeasureByModel(project)` (tokens/imputado por modelo).
- `batten show`: mostrar el modelo real de cada nodo; si el dominio declara `model` y el real difiere → marca `⚠ declared haiku, ran opus`.
- `batten measure`: sección por modelo — hace visible el ahorro del ruteo con números propios, no prometidos.

## Archivos

- `internal/spec/spec.go` — Models, `domains[].model`, validación
- `internal/store/store.go` — migración v2 (`verdicts.source`), case-fold en normPath, ModelsByNode, MeasureByModel, stale-run query
- `internal/hooks/hooks.go` — regla batten-source en verdictGate, auto-close en postToolUse, case-fold en writeSetGuard
- `cmd/batten/main.go` — cmdClose, cmdCheck, panic fence en cmdHook, doctor stale runs, show/measure con modelos
- `plugin/claude-code/commands/{batten-plan,batten-build,batten-verify}.md`, `skills/batten-engine/SKILL.md`
- `batten.schema.json`, `examples/agrosat/batten.yaml`

Reusar: `export.Run` (refresh al cerrar), `st.SaveVerdict`, patrón recover de `statusline.Run`, `humanTokens`.

## Verificación

1. Suite: `go build/vet/test ./...` limpios; migración: DB v1 abre → `user_version=2` + columna `source`.
2. **R1**: claim en run A → otra sesión denegada → `batten close A` → la otra sesión ya puede editar (claims liberados). Cerrar como ok sin veredicto → rechazado. Doctor avisa run stale (inyectar `started_at` viejo).
3. **R2**: gate con `checks: ['git --version', 'git bogus-cmd']` → `batten check` graba `blocked` citando el error real; commit → DENY "run batten check"; con checks que pasan → `ok` `source='batten'` → commit pasa (con veredicto de agente también ok).
4. **R3**: payloads basura (JSON malformado, tipos incorrectos, 10MB) a `batten hook` → exit 0, sin stack trace.
5. **R4**: claim `ml/f.py`, escribir `ML/F.PY` → DENY en Windows.
6. **M1**: spec con tiers; inyectar usage con modelos distintos por nodo → `batten show` los muestra y marca la desviación declarado-vs-real; `batten measure` desglosa por modelo; schema valida el ejemplo agrosat actualizado.
7. Commit por bloque (R1+R2, R3+R4, M1) y HANDOFF.md actualizado al cierre de cada uno.
