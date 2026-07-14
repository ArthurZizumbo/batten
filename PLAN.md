# batten — plan v2: de "parcialmente integrado" a completamente programado

## Contexto

El MVP (commit `1c1be26`) probó el core: gates que bloquean, contabilidad honesta, neutralidad. Pero la auditoría posterior encontró que **el ecosistema quedó a medias y la historia de instalación no existe**:

| Pieza | Estado real | Problema |
|---|---|---|
| Instalación en proyecto existente | **NO EXISTE** — `cmdInit` es un stub que imprime "corre /batten-init" | Es el caso de uso #1 de Arthur |
| Obsidian | 70% — funciona pero **solo manual** (`batten canvas`); 0 referencias a vault en hooks.go | El vault no se llena solo |
| graphify | 30% — solo prompts; sin staleness check, sin detección en init | Pointer, no integración |
| headroom | 10% — `measure: true` es un campo que **nada implementa** | Un campo que miente |
| engram | **REGRESIÓN** — los commands generalizados perdieron los `mem_search`/`mem_save` que el flujo original sí tenía | Se perdió en la generalización |
| Distribución | **ROTA** — `bin/` está vacío en git (gitignored); un install de marketplace no tendría binario | El error exacto de engram que criticamos |
| Windows hooks | Sin verificar — `${CLAUDE_PLUGIN_ROOT}/bin/batten` sin `.exe` puede no arrancar | Make-or-break en la máquina de Arthur |
| `agent_id` en PreToolUse real | Sin verificar — TODO el write-set guard cuelga de ese campo | Si no llega, el guard falla-abierto en silencio |

Objetivo: cerrar todo esto de modo que **instalar batten en un proyecto vivo sea un flujo real de 5 pasos**, y que cada integración declarada en `batten.yaml` tenga código detrás, no solo YAML.

---

## E0 — Spike de validación (PRIMERO; todo lo demás se ajusta a lo que salga)

Las tres incógnitas que pueden invalidar diseño. Se responden con el plugin **instalado de verdad** en Claude Code, no simulando hooks:

1. **Build + install local**: `go build -o plugin/claude-code/bin/batten.exe ./cmd/batten` → `/plugin marketplace add <ruta local del repo>` → `/plugin install batten@batten`. Crear un `batten.yaml` para **este mismo repo** (dogfood: batten gobernando el desarrollo de batten).
2. **¿Windows resuelve el binario en hooks exec-form?** Probar `${CLAUDE_PLUGIN_ROOT}/bin/batten` vs `batten.exe` vs shell-form (estilo engram, que corre bajo Git Bash — requisito de Claude Code en Windows). **Decidir con evidencia** la forma final de `hooks.json`.
3. **¿`PreToolUse` dentro de un subagente trae `agent_id`?** Lanzar un subagente real que haga un `Write` y capturar el payload (hook que loguea stdin a un archivo). 
   - Si SÍ → el guard queda como está.
   - Si NO → fallback ya diseñado: el guard pasa a modo advisory (inyecta `additionalContext` con la advertencia de colisión en vez de deny) y se documenta la limitación honestamente.
4. **Handshake MCP con cliente real**: el plugin ya declara `.mcp.json`; verificar que las tools `batten_*` aparecen y responden en la sesión. (Mi prueba anterior de una línea probablemente falló por framing/quoting, no por el server — los 13 tests del logic pasan.)
5. **TUI interactiva**: abrir `batten tui` en terminal real una vez.

Entregable: `docs/VERIFICATION.md` con los 5 resultados y las decisiones tomadas.

---

## P1 — Instalación en un proyecto existente (lo que "la verdad no está")

### 1a. `batten init` real (Go) — `cmd/batten/main.go` + nuevo `internal/scan/scan.go`

Hoy `cmdInit` es un stub. Se implementa el escaneo mecánico en Go:

- **Patrón de unidad**: escanear nombres de ramas recientes (`git for-each-ref`) buscando patrones `[A-Z]+-\d+` / `US-\d{3}` → proponer `unit.pattern` con lo que el repo ya usa.
- **Dominios**: top-level dirs con código; `check` desde targets reales (`Makefile` lint/test, `package.json` scripts, `pyproject.toml`, `go.mod`, `*.tf`) — **verbatim, no inventados**.
- **Skills/agents**: reutilizar `discovery.Skills/Agents/Suggest` (ya existe, `internal/discovery/discovery.go:103-161`) → proponer `domains[].skills` y `domains[].agent`.
- **Capacidades**: graphify en PATH o `graphify-out/` presente → proponer `graph.provider`; detectar `AGENTS.md`/`CLAUDE.md` por carpeta → `rules`.
- Salida: `batten.yaml` **borrador con comentarios y TODOs** donde hace falta juicio (invariants, gates). Nunca sobreescribe uno existente.
- Modo `--scan-json`: emite los hechos como JSON para que `/batten-init` (el agente) haga la parte de juicio — minar invariants de los AGENTS.md y migrar desde prosa con `--from`.

### 1b. Rampa de adopción: `enforcement: report | enforce` (nuevo, clave para proyectos vivos)

Un proyecto a mitad de sprint no puede recibir denials duros el día 1. Campo top-level en el spec (`internal/spec/spec.go`):

```yaml
enforcement: report   # los gates ADVIERTEN (additionalContext) en vez de denegar
```

- `internal/hooks/hooks.go`: `verdictGate` y `writeSetGuard` consultan `sp.Enforcement`; en `report` emiten la misma razón como advertencia visible, no como deny.
- `batten init` genera `enforcement: report` por default en proyectos existentes, con comentario "cambia a enforce cuando el equipo confíe".
- `batten doctor` reporta el modo con claridad ("los gates están en modo REPORT — nada bloquea todavía").

### 1c. El flujo de instalación documentado (README + `docs/INSTALL.md`)

```
1. /plugin marketplace add <repo>        # una vez por máquina
2. /plugin install batten@batten
3. /batten-init                          # o: batten init (borrador mecánico)
4. batten statusline --install           # opcional: habilita el techo de cuota
5. batten doctor                         # verde → listo; enforcement: report al inicio
```

Con sección explícita "adoptar en un proyecto en desarrollo" (report-mode, migración desde doc en prosa con `--from`, qué pasa con las ramas ya abiertas).

---

## P2 — Integraciones: que cada campo del YAML tenga código detrás

### 2a. Obsidian: export automático — nuevo `internal/export/export.go`

Extraer la lógica de `cmdCanvas` (main.go:~460) a `export.Run(sp, st, unitID)` = canvas.Render + vault.WriteRun + WriteBases. Llamarla desde:
- `hooks.stop()` (ya es `async: true` — costo cero en el turno) cuando hay vault configurado,
- `cmdVerdict` tras guardar un veredicto (el momento más valioso: el vault refleja el estado del gate),
- `cmdCanvas` (que queda como invocación manual del mismo camino).

Resultado: **el vault se llena solo** al final de cada turno/veredicto.

### 2b. engram: reinyectar los touchpoints perdidos (texto, no Go)

`plugin/claude-code/commands/*.md` + `skills/batten-engine/SKILL.md`, condicionados a `capabilities.memory.provider`:
- research/plan → `mem_search "[unidad/tecnología]"` antes de investigar (estaba en el flujo original, se perdió),
- verify → `mem_search` de bugs similares previos,
- close → `mem_save` con decisiones del run + `mem_session_summary`.

### 2c. graphify: profundizar doctor + init (Go, pequeño)

- `cmdDoctor`: **staleness** — mtime de `graphify-out/graph.json` vs `git log -1 --format=%ct`; avisar "el grafo tiene N commits de atraso; corre graphify --update o graphify hook install".
- `cmdDoctor`: si hay vault Y graphify → sugerir `graphify . --obsidian --obsidian-dir <vault>` (las tres memorias en un vault).
- `batten init`: detección ya cubierta en 1a.

### 2d. headroom: implementar `measure` de verdad (Go)

- **Detección**: al abrir un run (`EnsureRun` o `cmdPhase`), GET `http://127.0.0.1:8787/health` con timeout 250ms → columna nueva `headroom INTEGER` en `runs` (migración vía `PRAGMA user_version`, primera migración real del schema).
- **`batten measure`** (comando nuevo): agrupa runs cerrados por flag headroom; reporta media/mediana de tokens e imputed USD por grupo. Honestidad integrada: con n<3 por grupo dice "datos insuficientes para comparar", y siempre anota que las unidades difieren entre sí.
- `cmdDoctor`: si `compression.provider: headroom` y `measure: true` → reporta si el proxy está vivo ahora.

### 2e. Fix del doble conteo entre runs (store)

`usage` PK es `(request_id, run_id)`: el mismo transcript ingestado para dos runs abiertos cuenta doble. Cambiar a PK global `request_id` (un request pertenece a UN run). Va en la misma migración `user_version` que 2d.

---

## P2.5 — Concurrencia multi-sesión (dos Claude Code en paralelo sin chocar)

Escenario real: sesión A corre US-034, sesión B corre US-051, mismo repo (mismo working tree o worktrees). Hoy batten falla en tres puntos; el almacenamiento NO es el problema (WAL + `busy_timeout` + `SetMaxOpenConns(1)` ya aguantan escritores concurrentes) — el problema es **lógico**:

### 2.5a. Binding sesión↔run (el cimiento de todo lo demás)

Hoy `activeUnit()` (hooks.go) resuelve por rama y, si falla, por "el único run abierto". Con dos runs abiertos y una sola rama compartida → ambiguo → **el gate se apaga para ambas sesiones** (falla-abierto). Fix:

- `store`: nuevo `RunBySession(project, sessionID)` — los runs ya tienen `session_id` y `AdoptSession` ya existe.
- `activeUnit()` nueva prioridad: **(1) run abierto ligado a MI `session_id`** → esa es mi unidad, punto; (2) patrón en la rama (y al acertar, adopta la sesión); (3) único run abierto **sin sesión dueña** → usa + adopta; (4) ambiguo → sin gate, pero ahora **lo dice** (ver 2.5c).
- **Cómo nace el binding**: el hook `PreToolUse`(Bash) ya intercepta cada comando — cuando ve `batten phase <UNIT> ...` en `tool_input.command`, liga esa unidad a `in.session_id` (vía nuevo hook `PostToolUse` matcher Bash, que además ya nos sirve de ingest point). El CLI solo no puede saberlo (el subproceso Bash no recibe session_id); el hook sí.

### 2.5b. Write-set guard ENTRE runs (hoy solo defiende dentro de uno)

`writesets` PK es `(run_id, path)`: dos runs distintos pueden reclamar **el mismo archivo** y ambas sesiones lo editan sin fence. Fix:

- `store`: `WriteSetOwnerAcrossOpenRuns(project, path)` — busca el dueño en TODOS los runs abiertos del proyecto.
- `writeSetGuard`: si el archivo pertenece a un agente de **otro run abierto** → deny con mensaje que nombra a la otra unidad ("`ml/farslip/train.py` está siendo trabajado por US-034 en otra sesión").
- **Extensión al main-loop**: hoy solo se fencea a subagentes (`agent_id != ""`). Con el binding 2.5a, un `Write` del loop principal de la sesión B (sin agent_id, pero con session_id → run B) sobre un archivo de un run A abierto → deny/warn según `enforcement`.
- `claim` respeta lo mismo: reclamar un archivo ya reclamado por otro run abierto falla con el nombre de la unidad en conflicto.

### 2.5c. Visibilidad: cada sesión sabe quién es

- `sessionStart` inyecta: "esta sesión está ligada a **US-034** (fase verify)" o, si ambiguo: "hay 2 unidades abiertas (US-034, US-051) y esta sesión no está ligada a ninguna — corre `batten phase <unit> <fase>` para ligarla; **los gates no pueden actuar hasta entonces**". La ambigüedad deja de ser silenciosa.
- `batten runs` gana columna SESSION (qué sesión trabaja cada unidad).
- `batten doctor`: si detecta 2+ runs abiertos sin binding → recomendar **un worktree por unidad paralela** (rama propia → la atribución por rama vuelve a funcionar sola; `dbPath()` es global, así que el ledger y el guard cruzado ya son compartidos entre worktrees; los write-sets son repo-relativos → comparables).

### 2.5d. Tests

Dos session_ids sintéticos, dos runs: (a) binding por prioridad (sesión > rama > único-sin-dueño > ambiguo); (b) run B reclama archivo de run A abierto → falla nombrando a A; (c) Write del main-loop de B sobre archivo de A → deny en enforce / warn en report; (d) cerrar run A libera sus archivos para nuevos claims.

## P3 — Distribución del binario (arreglar el error-de-engram que reprodujimos)

- **Track A (ya, para Arthur/desarrollo)**: `scripts/build-plugin.ps1` + `.sh` (build → `bin/`), documentado en INSTALL.md. El marketplace local (`/plugin marketplace add <ruta>`) funciona con el binario recién compilado.
- **Track B (release)**: `.goreleaser.yaml` ya existe (del fan-out) + nuevo `.github/workflows/release.yml`. Bootstrap `scripts/bootstrap.sh` como hook `SessionStart` (shell-form → corre bajo Git Bash también en Windows): si `${CLAUDE_PLUGIN_DATA}/bin/batten[.exe]` no existe, descargarlo del release correcto para el OS/arch. `hooks.json` apunta a `${CLAUDE_PLUGIN_DATA}/bin/...` (la sustitución en hook commands está documentada). **La forma exacta se decide con la evidencia del E0.**
- Los hooks ya no-op en silencio si el binario falta (verificado), así el primer arranque degrada en vez de romper.
- Nota: el module path `github.com/arthu/batten` es placeholder — confirmar org/nombre real antes del primer release (decisión de Arthur, no bloquea nada local).

---

## P4 — Verificación end-to-end (repetir + nuevos)

1. Suite completa: `go build ./... && go vet ./... && go test ./...`.
2. E2E existente del sandbox (gates, guard, canvas, contabilidad con subagentes, statusline, neutralidad) — sin regresiones.
3. **Nuevos**:
   - `batten init` sobre el sandbox webapp TS → yaml borrador válido (valida contra `batten.schema.json`), `doctor` limpio, con `enforcement: report`.
   - Modo report: commit sin veredicto → **advertencia visible, no deny**; flip a `enforce` → deny.
   - Vault automático: simular hook `Stop` → nota + canvas + bases aparecen sin comando manual.
   - `batten measure`: runs sintéticos con/sin flag → reporte compara; con n<3 dice "insuficiente".
   - Migración: DB del MVP abre limpia con el schema nuevo (user_version 0→1).
   - Doble conteo: mismo transcript a dos runs → el segundo ingest no duplica.
   - **Multi-sesión**: los 4 tests de 2.5d + escenario integrado (dos "sesiones" simuladas, dos unidades, cada gate actúa solo sobre la suya; colisión de write-set entre runs denegada nombrando a la otra unidad).
   - Plugin real (E0): los 5 puntos del spike documentados en `docs/VERIFICATION.md`.

## Orden de ejecución

**Primera acción tras aprobar: copiar este plan a `PLAN.md` del repo** (Arthur lo pidió guardado ahí).

**E0 primero** (media sesión; sus hallazgos ajustan P1/P3) → P1 (init + rampa + docs) → P2 (integraciones) → **P2.5 (multi-sesión)** → P3 (distribución) → P4 (verificación) → commit por fase.

## Archivos críticos

- Nuevos: `internal/scan/scan.go`, `internal/export/export.go`, `scripts/build-plugin.{ps1,sh}`, `scripts/bootstrap.sh`, `.github/workflows/release.yml`, `docs/INSTALL.md`, `docs/VERIFICATION.md`
- Modificados: `cmd/batten/main.go` (cmdInit real, cmdMeasure, doctor+, columna SESSION en runs), `internal/spec/spec.go` (enforcement), `internal/hooks/hooks.go` (report-mode, export en stop, binding sesión↔run, guard cruzado, hook PostToolUse), `internal/store/store.go` (migración user_version: headroom flag + PK usage; `RunBySession`, `WriteSetOwnerAcrossOpenRuns`), `plugin/claude-code/commands/*.md` (engram), `plugin/claude-code/hooks/hooks.json` (según E0 + PostToolUse), `README.md`
- Reutilizar: `discovery.Suggest/Skills/Agents`, `statusline.Install/Installed`, `vault.Writer`, `canvas.Render` — todo ya existe y está testeado.
