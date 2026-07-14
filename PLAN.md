# batten — plan

## Contexto

Arthur tiene un flujo de trabajo que funciona (`prompts_optimizers_v2.local.md`, 700 líneas de prosa) y quiere generalizarlo a un plugin reutilizable en cualquier proyecto. El documento tiene dos defectos:

1. **Está clavado a AgroSat** — `make check`, `docs/us-XXX/`, `nvidia-smi`, `session_id en toda query`. Otro proyecto = reescribir.
2. **Es prosa, y la prosa no obliga.** Sus dos reglas más importantes (`prohibido result:ok con evidence[] vacío`; `dos agentes NUNCA escriben el mismo archivo`) son súplicas que el agente puede ignorar sin consecuencia.

**Tesis**: hay tres memorias en un agente de coding. *Estructural* (qué ES el código → graphify) y *episódica* (qué DECIDIMOS → engram) ya están resueltas. La tercera, **procedural** (CÓMO TRABAJAMOS), no la tiene nadie. Ese es el producto: convertir el proceso en un archivo declarativo (`batten.yaml`) y hacerlo cumplir con hooks.

Diseño completo y correcciones al doc v2 en `DESIGN.md`.

---

## Estado actual — qué YA existe y funciona

~2.500 LOC de Go, compilando, con los gates **verificados bloqueando de verdad** en un repo sandbox:

| Archivo | LOC | Estado |
|---|---|---|
| `internal/spec/spec.go` | 338 | ✅ parser + validador de `batten.yaml` |
| `internal/store/store.go` | 544 | ✅ SQLite (WAL + `SetMaxOpenConns(1)`), runs/nodes/edges/writesets/verdicts/ledger/overrides |
| `internal/hooks/hooks.go` | 377 | ✅ verdict gate + write-set guard + ingesta del DAG |
| `internal/canvas/canvas.go` | 231 | ✅ emisor JSON Canvas 1.0 (validado contra el spec) |
| `cmd/loom/main.go` | 658 | ✅ CLI: init/doctor/hook/phase/claim/verdict/runs/show/canvas/budget/override |
| `internal/tui/tui.go` | 359 | ⚠️ escrito, **sin compilar ni cablear** |
| `examples/agrosat/loom.yaml` | — | ✅ el flujo de AgroSat migrado a spec |
| `plugin/claude-code/` | — | ✅ plugin.json, hooks.json, 2 skills, 1 command |

**Verificado en sandbox** (no es aspiracional):
- `git commit` sin veredicto → `permissionDecision: deny`
- veredicto `ok` con `evidence: []` → rechazado por el binario
- veredicto `blocked` → commit denegado, con el `why` citado
- veredicto `ok` con 3 evidencias → commit permitido
- agente C escribe archivo de agente A → denegado, con el write-set propio listado
- `.canvas` emitido y validado contra JSON Canvas 1.0

---

## Cambios acordados

1. **Nombre: `loom` → `batten`.** `loom` está quemado (Loom video / Project Loom del JDK) y media docena de nombres de tejido ya los usan **competidores directos** (`weftlabs/weft-cli`, `goweft/heddle`, `mindfold-ai/trellis` — todos herramientas de workflow para Claude Code). `batten` = la pieza del telar que *golpea la trama a su lugar*, e inglés náutico *"batten down"* = asegurar. Namespace limpio.

2. **El presupuesto cambia de "tope en dólares" a "utilización de la suscripción".** Con suscripción el costo marginal es cero; la métrica correcta es cuánto de la cuota se quema y cuánto valor se extrae. Ver abajo.

---

## Lo que falta construir

### 1. Rename `loom` → `batten` (mecánico)
`cmd/loom/` → `cmd/batten/`, module path, todos los identificadores, `loom.yaml` → `batten.yaml`, `.loom/` → `.batten/`, skills/commands.

### 2. Contabilidad real — el hueco grande

Hoy `store.Charge()` existe pero **nada lo llama**: el presupuesto siempre marca $0.00. El wedge del "budget governor" es decorativo. Tres hallazgos de la investigación mandan el diseño:

- **Los hooks NO reciben tokens ni costo.** Solo `transcript_path`.
- **Los subagentes viven en archivos SEPARADOS**: `~/.claude/projects/<proj>/<sessionId>/subagents/agent-<id>.jsonl` (+ `.meta.json` con `agentType`). El `toolUseResult` del padre **no trae totales**. Leer solo `transcript_path` **subcuenta brutalmente** los runs con fan-out — que es exactamente nuestro caso.
- **La cuota de suscripción solo la expone el `statusLine`**, no los hooks: `rate_limits.five_hour.used_percentage` + `resets_at`, y `seven_day`.

**Diseño:**

**`internal/usage/`** — parser de transcript:
- camina el transcript del padre **y** `<sessionId>/subagents/*.jsonl`
- dedup por `requestId`; ignora `model: "<synthetic>"`
- agrupa por `message.model` × `usage.speed`, suma los 5 buckets: `input_tokens`, `output_tokens`, `cache_creation.ephemeral_5m_input_tokens`, `cache_creation.ephemeral_1h_input_tokens`, `cache_read_input_tokens`
- **no** usa `total_cost_usd` (bug conocido de ~10×, issue #53371); calcula el costo imputado con tabla de precios propia
- multiplicadores: 5m-write ×1.25, 1h-write ×2, cache-read ×0.1, fast-mode (Opus 4.8 = $10/$50), `inference_geo:us` ×1.1, web search $10/1k
- atribución por subagente vía `subagents/*.meta.json`

**`batten statusline`** — cara nueva del binario, **la única forma de leer la cuota**:
- **verificado (Context7)**: `statusLine` vive en `settings.json` del usuario o del proyecto — **un plugin NO lo registra automáticamente**. Instalación explícita y consentida: `batten init` detecta si hay statusLine configurado; si no, ofrece escribir `{"statusLine":{"type":"command","command":"batten statusline"}}` en `.claude/settings.json` del proyecto (nunca sobreescribe uno existente — si ya hay uno, ofrece modo *chain*: batten llama al comando previo y le antepone su segmento)
- en cada invocación: snapshot de `rate_limits.{five_hour,seven_day}.{used_percentage,resets_at}` + `session_id` a SQLite (keyed por sesión; la cuota es global de la cuenta, el delta se atribuye al run activo)
- imprime el status line **con el estado de batten**: unidad activa, fase, veredicto, y consumo de cuota
- doble función: es el **sensor de cuota** y el display
- **degradación honesta**: sin statusLine instalado, `batten budget` reporta tokens/costo imputado (del transcript) y marca la cuota como "no disponible — instala el statusline", nunca la inventa

**Spec nuevo:**
```yaml
budget:
  tokens_per_run: 2_000_000     # duro, sin ambigüedad
  imputed_usd_per_run: 8.00     # lo que HABRÍA costado en API = valor extraído
  quota_pct_per_run: 15         # % de la ventana de 5h que este run puede quemar
  on_exceed: block
```

**Nota de honestidad**: Anthropic **no publica** las cuotas absolutas de Pro/Max. Solo se puede confiar en **porcentajes**, nunca en números absolutos de tokens de cuota. El tool debe decirlo, no inventarlo.

### 3. Cablear la TUI — con dos fixes de API verificados
`internal/tui/tui.go` existe (árbol + detalle + barra de cuota) pero usa la API v2 mal en dos puntos (verificado contra el upgrade guide vía Context7):
- `Init()` en v2 devuelve **solo `tea.Cmd`**, no `(tea.Model, tea.Cmd)` — corregir firma
- `AltScreen` ya no es opción de programa (`tea.WithAltScreen()`): es campo del `View` (`v.AltScreen = true`) — mover
- imports `charm.land/bubbletea/v2` + `charm.land/lipgloss/v2` (ya confirmé que los paths `github.com/...` v2 no resuelven)

Luego: `case "tui"` en main, probar sobre el sandbox. **No** se construye layout de grafo node-link — eso lo hace el `.canvas` en Obsidian; la TUI es para revisar (árbol, veredicto, cuota) sin salir de la terminal.

### 4. Servidor MCP
`batten mcp` — expone el grafo al agente: `query_runs`, `run_graph`, `verdict_status`, `budget_status`, `writeset_owner`. Va en `.mcp.json` del plugin (`command: ${CLAUDE_PLUGIN_ROOT}/bin/batten`, nunca nombre pelado — el error de engram). SDK: **`modelcontextprotocol/go-sdk` (oficial)**; engram usa `mark3labs/mcp-go`, que fue el estándar de facto pre-SDK — preferir el oficial salvo que falte algo concreto.

### 5. Completar el plugin
- `commands/`: `batten-plan.md`, `batten-build.md` (el orquestador de fan-out), `batten-verify.md`, `batten-close.md`, `batten-night.md` (el Mega Prompt generalizado)
- `batten.schema.json` (JSON Schema del spec, para editores)
- `bin/` multi-plataforma vía GoReleaser (`CGO_ENABLED=0`)
- README

### 6. Descubrimiento de skills y subagentes del usuario

`batten.yaml` referencia skills (`domains[].skills`, `gates[].skills`) y agentes; batten debe descubrirlos y validarlos, no asumirlos:

- **Superficies de escaneo**: `~/.claude/skills/*/SKILL.md` (usuario), `.claude/skills/` (proyecto), skills de plugins instalados (`plugin:skill`); subagentes custom en `.claude/agents/*.md` y `~/.claude/agents/*.md` (frontmatter `name`/`description`/`tools`/`model`); comandos en `.claude/commands/`.
- **`batten init`**: escanea y *propone* el mapeo skill→dominio en la entrevista (equivalente a la tabla "skills a activar según lo que toque la US" de la Fase 2 del flujo AgroSat). El usuario edita; el spec es suyo.
- **`batten doctor`**: valida que cada skill/agent referido en el yaml exista — un skill renombrado avisa en doctor, no falla en silencio a mitad de un run nocturno.
- **Campo nuevo `domains[].agent`**: nombre de un subagente custom del usuario para ese dominio (en vez de general-purpose). El orquestador de fan-out lo usa al lanzar; los `domains[].skills` van en el prompt del subagente.
- **Etiquetado del DAG**: `SubagentStart.agent_type` = el nombre del agente custom → nodos, TUI y `.canvas` muestran los nombres del usuario. Los matchers de hooks también pueden filtrar por ellos.
- **Skills de gate como evidencia**: los `gates[].skills` (p.ej. un security-audit) corren en la fase verify y sus hallazgos se citan en `evidence[]` del veredicto.

### 7. Obsidian — el vault donde conviven las tres memorias

Esto es lo que supera al artículo de Medium (que solo mete el grafo de código al vault): **un vault con las tres memorias enlazadas por wikilinks**.

Módulo `internal/vault/` — escribe **archivos directo al vault** (verificado: el CLI de Obsidian exige la app abierta → inútil para hooks/headless; los `.md`/`.base`/`.canvas` son solo archivos):

- **Por run**: `loom/<proyecto>/runs/US-034.md` con frontmatter tipado (`unit`, `status`, `phase`, `verdict`, `evidence_count`, `tokens`, `imputed_usd`, `base_sha`) + wikilinks: `[[US-033]]` (unidad previa), `[[UserService]]` (nodos del grafo de graphify que el run tocó — mismo vault = los links resuelven), y el `![[US-034.canvas]]` embebido.
- **El `.canvas` por run** (ya construido y validado): fases, fan-out, veredicto, colores por estado.
- **Dashboards `.base`** (solo-humanos): "Runs por estado", "Presupuesto imputado por semana", "Veredictos blocked pendientes". **Caveat verificado**: los rows renderizados de un `.base` NO son legibles por scripts — `.base` es vista humana; SQLite sigue canónico. Nunca scrapear Bases.
- **Dependencia blanda de `kepano/obsidian-skills`** (MIT): `batten doctor` sugiere instalarlo para que el agente autore `.md`/`.base`/`.canvas` correctos; batten NO lo requiere (los archivos los escribe el binario en Go, con formato fijo).

Resultado en el graph view de Obsidian: el run `US-034` enlaza a los módulos que tocó (graphify), a las decisiones que lo motivaron (engram exporta al mismo vault si se configura), y a las unidades vecinas. **Estructural + episódico + procedural, un solo grafo navegable.**

### 8. graphify — cableado concreto (opcional, degrada a grep)

- **`batten init`** detecta `graphify` en PATH y/o `graphify-out/graph.json` existente → propone `capabilities.graph.provider: graphify`.
- **Fase `plan` (`graph_query: true`)**: el skill/command de la fase instruye `graphify query "..."` y `graphify path A B` en lugar de `grep -r` + N×Read — ahí es donde el flujo AgroSat paga hoy 15-20k tokens de re-orientación. Sin graphify: cae a grep, el spec no falla.
- **Vault compartido**: si `capabilities.obsidian.vault` está definido, `batten doctor` sugiere `graphify . --obsidian --obsidian-dir <vault>` para que el mapa del código viva en el MISMO vault que los runs → los wikilinks de los run-notes a nodos de código resuelven.
- **`lessons: false` obligatorio en el default**: la capa lessons/reflect de graphify pisa a engram (y es su parte más débil, verificado en la investigación).
- **NO usamos su hook `PreToolUse`** (se rompió con Claude Code ≥2.1.117): batten tiene los suyos.
- **Staleness**: `batten doctor` compara mtime de `graph.json` vs el HEAD del repo y avisa si el grafo está viejo (graphify tiene `hook install` de git para esto; sugerirlo, no imponerlo).

### 9. headroom (opcional, medido)
`capabilities.compression.measure: true` → comparar tokens del transcript con/sin headroom **en el fan-out real**, porque hay duda abierta (documentada) de si sus hooks cubren subagentes de la Agent tool. `memory: false` siempre (engram es dueño de lo episódico). Instalación vía `headroom init claude`, nunca modo MCP (comprime post-facturación — verificado en sus docs).

---

## Criterio de aceptación (bloqueante)

**Neutralidad**: el mismo binario corre en AgroSat y en un repo web TS no relacionado, y **lo único que difiere es el `batten.yaml`**. Si hay que tocar código Go para el segundo repo, el diseño falló.

---

## Verificación end-to-end

1. `go build ./... && go vet ./...`
2. Sandbox (el script ya usado): gate deniega sin veredicto → rechaza `ok` sin evidencia → deniega en `blocked` → permite en `ok` con evidencia → write-set guard deniega la colisión → `.canvas` valida contra el spec 1.0. **Nuevo**: `hyperfine` sobre `batten hook PreToolUse` con y sin `batten.yaml` presente (fast path <50ms).
3. **Contabilidad**: correr un fan-out real de 2+ subagentes en este repo; verificar que `batten budget` cuenta los tokens de los **subagentes** (no solo del padre) comparando contra `/usage` de Claude Code.
4. **Cuota**: registrar `batten statusline`, correr una sesión, confirmar que `rate_limits.five_hour.used_percentage` queda en SQLite y que el delta antes/después del run es coherente.
5. **TUI**: `batten tui` sobre el sandbox; ver el árbol, el veredicto y la barra de cuota.
6. **Neutralidad**: `batten init` en un repo TS cualquiera; `batten doctor` limpio sin tocar Go.
7. **Skills/agents**: crear un subagente custom en `.claude/agents/` y un skill de proyecto; `batten init` los detecta y propone el mapeo; `batten doctor` acusa un skill inexistente referido en el yaml; un fan-out con `domains[].agent` custom etiqueta el nodo con ese nombre en `batten show` y el `.canvas`.
8. **Vault**: configurar un vault de prueba; correr un run completo en sandbox; abrir Obsidian y verificar: la nota del run con frontmatter, el `.canvas` embebido renderizado, el `.base` de "Runs por estado" mostrando el run, y (con graphify exportado al mismo vault) un wikilink de la nota del run a un nodo de código que resuelve.

---

## Detalles de implementación que importan (salieron de la revisión)

- **Latencia del hook**: `PreToolUse` dispara en CADA Bash/Write/Edit → spawn del binario + open SQLite cada vez. Presupuesto: <50ms. Fast path: si `spec.Find()` no halla `batten.yaml` subiendo directorios, salir antes de abrir la DB. Medir con `hyperfine` en la verificación.
- **¿Cuándo se ingesta el usage?** En `SubagentStop`/`Stop` (ya son `async: true` — no bloquean al agente) + bajo demanda con `batten budget --refresh`. El gate de presupuesto lee el ledger ya materializado; nunca parsea JSONL en el camino crítico del `PreToolUse`.
- **Windows + hooks exec form**: `${CLAUDE_PLUGIN_ROOT}/bin/batten` vs `batten.exe` — verificar si Claude Code resuelve la extensión en Windows; si no, `hooks.json` necesita el `.exe` o un shim. Item explícito de verificación, no suposición.
- **Multi-sesión**: dos sesiones de Claude Code en el mismo repo escriben el mismo `batten.db` (WAL lo aguanta); los snapshots de statusline van keyed por `session_id` y el delta de cuota se atribuye solo al run cuya sesión lo generó.

## Riesgos

- **El formato del transcript JSONL no es API pública.** Puede cambiar sin aviso. Mitigación: si el parseo falla, contar 0 y **avisar**, nunca romper la sesión ni mostrar un número inventado.
- **Sobre-declarar el spec.** Si `batten.yaml` intenta expresar todo se vuelve un DSL que nadie escribe. Regla: *si no lo puede hacer cumplir un hook o ejecutar un comando, no va en el yaml.*
- **El gate como fricción.** Un gate que bloquea mal es peor que ninguno. Por eso `batten override --reason` existe, es obligatorio dar razón, y queda en el log de auditoría.
- **graphify/headroom son pre-1.0.** Por eso son capacidades opcionales que degradan, no dependencias duras.
