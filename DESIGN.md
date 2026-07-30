# loom — memoria procedural como dato

> ## ⚠️ Documento histórico. La herramienta se llama **batten**.
>
> Este archivo es el documento de diseño **v3, del 2026-07-14**, escrito antes del rename. Se
> conserva sin reescribir porque es un registro fechado: reescribirlo hacia atrás lo volvería una
> falsificación. Al leerlo, traducí:
>
> | dice | es |
> |---|---|
> | `loom` | `batten` |
> | `loom.yaml` | `batten.yaml` |
> | AgroSat, FarSLIP, US-0NN | el proyecto privado sobre el que se dogfoodeó |
>
> **Esto no es documentación de uso y puede estar desactualizado.** Lo que la herramienta hace hoy
> está en [README.md](README.md); cómo instalarla, en [docs/INSTALL.md](docs/INSTALL.md); qué
> cambió y cuándo, en [CHANGELOG.md](CHANGELOG.md); y lo que sigue abierto, en
> [ROADMAP.md](ROADMAP.md). Las decisiones de §1 y la tesis de §0 siguen siendo la razón de ser del
> proyecto — eso es lo que vale la pena leer acá.

> **v3** del linaje `ecosistema-agentico-2026`. El v2 preguntaba "¿cómo generalizo mi harness en un producto?".
> El v3 responde con una tesis más estrecha y más defendible: **el hueco no es observabilidad, es memoria procedural.**
>
> **Fecha**: 2026-07-14. Basado en investigación verificada de `kepano/obsidian-skills`, `Graphify-Labs/graphify`,
> `headroomlabs-ai/headroom`, los docs de plugins/hooks de Claude Code, y el código real de `Gentleman-Programming/engram`.
> **Corrige seis cosas del v2** (§1) y **mata el ítem más caro de su roadmap** (§7).

---

## 0. TL;DR

Un agente de coding tiene tres memorias. Dos ya están resueltas. La tercera no existe.

| Plano | Qué recuerda | Cómo se forma | Quién lo tiene |
|-------|--------------|---------------|----------------|
| **Estructural** | qué **ES** el código, ahora | se **reconstruye** (AST, determinista) | graphify |
| **Episódico** | qué **DECIDIMOS**, a lo largo del tiempo | se **acumula** (observaciones) | engram, claude-mem |
| **Procedural** | **CÓMO TRABAJAMOS** | se **declara** | **nadie** |

El artículo de Medium que originó esto (Graphify + Obsidian, "70x menos tokens") arma el plano 1 y lo llama memoria.
Es media foto. Un agente que sabe perfectamente qué es tu código y qué decidiste ayer **sigue sin saber cómo trabajas**:
qué fases corre, qué se paraleliza, qué es un write-set disjunto, qué cuenta como evidencia, qué bloquea un cierre.

Hoy eso vive en prosa (`prompts_optimizers_v2.local.md`, 700 líneas, clavado a un proyecto), se pega a mano en cada
sesión, y **no se puede hacer cumplir**: "prohibido `APPROVE` con `evidence[]` vacío" es una regla que el agente puede
ignorar sin consecuencia.

**`loom` convierte esa prosa en un spec declarativo (`loom.yaml`) + un motor que lo ejecuta y lo hace cumplir por hooks.**

Un binario Go puro + un plugin delgado de Claude Code. No re-orquesta al agente: **le pone los rieles**.

---

## 1. Correcciones al v2 (`ecosistema-agentico-2026`)

La investigación de julio forzó seis correcciones. Documentarlas evita repetir la imprecisión.

| # | Sección v2 | Lo que decía | Corrección verificada (jul-2026) |
|---|-----------|--------------|----------------------------------|
| 1 | §8 TUI | "no existe librería Go que haga layout Sugiyama→ASCII; construir la TUI de grafo (~semanas) es el wedge" | **Cierto, e irrelevante.** El spec **JSON Canvas 1.0** está completo y es trivial (nodos `id/type/x/y/width/height/color`, edges `fromNode/toNode/fromSide/toEnd/label`). Emitir el run-DAG como `.canvas` son ~200 LOC y **Obsidian lo renderiza gratis**, navegable y git-diffable. **La TUI deja de ser el MVP y pasa a ser fase opcional.** El wedge no era dibujar el grafo — era *tener* el grafo. |
| 2 | §10 obsidian-bridge | "construir el puente al vault (módulo Go, shell al CLI de Obsidian)" | **No construir.** (a) graphify ya exporta vault + `graph.canvas` nativo (`--obsidian`). (b) El CLI de Obsidian **requiere la app de escritorio corriendo** — inservible headless/CI. **Escribir archivos directo al vault** (que es lo que `kepano/obsidian-skills` habilita: `.md`/`.base`/`.canvas` son solo archivos). El CLI queda como lujo interactivo, no como dependencia. |
| 3 | §5 distribución | "forma = engram exacto" | **Engram se equivoca aquí y no hay que copiarlo.** `bin/` de un plugin **sí** se añade al PATH del Bash tool automáticamente (docs, "File locations reference"). Engram no lo usa: instala el binario fuera de banda (brew / `go install`) y **si el usuario no lo instaló, sus hooks fallan en silencio**. Además sus hooks son shell scripts que dependen de `bash`+`jq`+`curl` → frágiles en Windows. **loom embarca el binario y lee el JSON del hook de stdin en exec form: cero dependencias de shell.** |
| 4 | §14 headroom (ausente en v2) | — | **Evaluado y admitido como opcional.** El issue #951 (el daemon pre-forkeado no heredaba `ANTHROPIC_BASE_URL`) **está cerrado desde 2026-06-18**; la integración correcta hoy es `headroom init claude` (hooks `SessionStart`/`PreToolUse`, `supervisor_kind: none`). Su memoria es **opt-in**, así que no pisa a engram si no pasas `--memory`. Su **CacheAligner preserva el prompt cache** (96% hit medido independientemente). **PERO**: la ganancia real en coding es **~15-25%, no 60-95%** (el 95% es JSON), y hay una duda sin resolver sobre si ahorra algo en workflows de fan-out. → capacidad opcional **instrumentada**, no pieza de arquitectura (§9). |
| 5 | §6.1 SQLite | "una goroutine single-writer + busy_timeout" | Incompleto. Falta lo que engram sí hace y casi todos omiten: **`db.SetMaxOpenConns(1)`**. Sin eso, el pool de `database/sql` abre varias conexiones al mismo archivo y te da `SQLITE_BUSY` **contra ti mismo**. Los 4 PRAGMA + `SetMaxOpenConns(1)` son el patrón completo. |
| 6 | §2 "el producto es observabilidad" | el wedge era "loop + budget governor + DAG-TUI" | **El wedge es más estrecho y mejor: el spec.** La observabilidad es *consecuencia* de tener el proceso declarado, no el producto. Un DAG-TUI sin spec es un visor de logs bonito; un spec sin TUI ya bloquea cierres sin evidencia y ya impide colisiones de write-set. **El valor está en el `loom.yaml`, no en el grafo.** |

---

## 2. El producto en una frase

> **`loom.yaml` es a tu proceso lo que `Dockerfile` es a tu entorno**: un archivo declarativo, versionado,
> diffeable, que convierte una práctica implícita en un artefacto ejecutable y verificable.

Todo lo demás (binario, hooks, SQLite, canvas, MCP, TUI) existe para **servir** a ese archivo.

---

## 3. La generalización — qué es genérico y qué es del proyecto

Tu flujo de 7 fases **ya es una máquina de estados genérica disfrazada de prosa**:

```
research → plan → build(fan-out) → verify(gate) → fix → reverify(gate) → close
```

Eso es universal. Sirve para AgroSat, para un SaaS TypeScript, para un repo de investigación.
Lo que es de AgroSat es **puro dato**:

- la lista de skills por dominio
- las reglas por capa (`session_id` en toda query, Polars no pandas, i18n trilingüe)
- los comandos del checker (`make check`, `pytest`, `pnpm lint`)
- las rutas de artefactos (`docs/us-*/`)
- el recurso en contención (la H100) y su orden de prioridad
- la llave de provenance (`US + git_sha + mlflow + dvc`)

**Todo eso sale al `loom.yaml`. El motor no sabe qué es FarSLIP, ni una H100, ni una US.**

### El test de neutralidad

El core es neutral si y solo si **el mismo binario corre en dos repos no relacionados y lo único que difiere es
el `loom.yaml`**. Ese es el criterio de aceptación de la Fase 1, y se dogfoodea en AgroSat como *primer adapter*,
no como caso especial.

---

## 4. El spec (`loom.yaml`)

```yaml
version: 1
project: agrosat

# El sustantivo de la unidad de trabajo. US / ticket / issue / story.
unit:
  name: US
  pattern: 'US-\d{3}'
  plan: context/RefinamientoPlaneacionAgroSatCopilot_v8.md
  locator: '### {id}'              # cómo hallar el bloque de la unidad en el plan

# Dónde escribe cada fase. {id} se sustituye.
artifacts:
  research: docs/us-research/{id}.md
  planning: docs/us-planning/{id}.md
  handoff:  docs/us-handoff/{id}.md
  manual:   docs/manual-test/{id}.md
  resolved: docs/us-resolved/{id}.md

# La máquina de estados. Los nombres son tuyos; la mecánica es del motor.
phases:
  - id: research
    optional: true                 # solo si hay tecnología nueva
  - id: plan
    reads: [research]
    graph_query: true              # consulta el grafo en vez de grepear
  - id: build
    fanout: true                   # <-- la fase de fan-out
    reads: [plan]
    anchor: git_sha                # registra el SHA base: ancla del diff
  - id: verify
    gate: qa
    diff_from: anchor              # opera SOLO sobre el diff de la unidad
  - id: fix
    interactive: true
  - id: reverify
    gate: qa
    when: fix.changed_logic
  - id: close
    requires_verdict: ok           # GATE DURO (hook, no convención)

# Los dominios = los ejes del fan-out. LA ÚNICA parte específica del proyecto.
domains:
  backend:
    path: backend/
    rules: backend/AGENTS.md
    check: ['make lint', 'make test']
    coverage: 70
    skills: [agrosat-backend-api, agrosat-backend-services]
    invariants:
      - session_id en toda query/endpoint
      - Depends(get_current_user) en protegidos
      - lógica en service, no en router
  ml:
    path: ml/
    exclude: [ml/agent/]
    rules: ml/AGENTS.md
    check: ['poetry run ruff check .', 'pytest']
    skills: [agrosat-ml-segmentation, agrosat-ml-features]
    resources: [gpu]               # declara contención -> el motor serializa
    invariants:
      - Polars, no pandas en pipelines
      - Spatial CV (build_spatial_kfold), no random split
      - MLflow con data_version + code_version

# Recursos compartidos que fuerzan serialización entre agentes.
resources:
  gpu:
    kind: exclusive_pool
    probe: 'nvidia-smi --query-gpu=memory.free --format=csv,noheader'
    unit: MiB
    priority: [farslip, tsvit, ensembles, qwen]   # orden cuando no caben

# El gate.
gates:
  qa:
    checks: ['make check', 'pnpm lint', 'make test', 'pnpm test']
    skills: [agrosat-security-audit, agrosat-code-review]
    verdict: required
    evidence: required             # evidence[] vacío -> blocked. No negociable.

# Llave de provenance: qué ancla una unidad cerrada a evidencia reproducible.
provenance:
  format: '{id} @ {git_sha7} + mlflow:{mlflow_run} + dvc:{dvc_rev}'

# Budget governor — lo que Claude Code no tiene.
budget:
  usd_per_run: 5.00
  max_iterations: 3
  on_exceed: block                 # block | warn | downgrade_effort

# Capacidades opcionales. Degradan con gracia si faltan.
capabilities:
  graph:
    provider: graphify             # | none
    query_before_read: true
    lessons: false                 # engram es dueño de lo episódico
  memory:
    provider: engram               # | claude-mem | none
  obsidian:
    vault: ~/vaults/agrosat
    export: [runs, verdicts, canvas]
  compression:
    provider: headroom             # | none
    memory: false                  # apagada: engram es dueño
    measure: true                  # instrumentar: ¿ahorra de verdad en NUESTRO fan-out?
```

**Nada en el motor conoce estas palabras.** El motor lee `domains[*].check` y lo ejecuta; no sabe qué es `pytest`.

### `loom init` — el arranque que hace que esto sea usable

El spec solo es "general" si migrar a él es barato. `loom init` **entrevista al repo**:

- detecta lenguajes, gestores de paquetes, targets de `Makefile`, `AGENTS.md`/`CLAUDE.md` por carpeta
- detecta skills instalados y los mapea a dominios
- `loom init --from docs/general/prompts_optimizers_v2.local.md` → **lee tu flujo en prosa y propone el `loom.yaml`**

Migrar AgroSat es un comando, no una reescritura.

---

## 5. Los tres wedges — lo que la prosa no puede y un hook sí

Esto es lo único que se construye. Todo lo demás se reutiliza.

### 5.1 Verdict gate real

Hoy la regla de oro ("prohibido `ok` con `evidence[]` vacío") es una **súplica**. El agente puede cerrar igual.

`PreToolUse` con matcher `Bash`, inspeccionando `tool_input.command`: si es un `git commit` y no existe un veredicto
`ok` con `evidence[]` no vacío para la unidad activa → **deny**.

```json
{ "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "US-034 no tiene verdict envelope con result=ok. Corre la fase verify. (loom)"
}}
```

No es una convención. **Es imposible de saltarse.**

### 5.2 Write-sets disjuntos verificados

Hoy "dos agentes NUNCA escriben el mismo archivo" es **disciplina**. Un agente distraído la rompe y te enteras en el merge.

El motor conoce el write-set de cada sub-agente (lo declara el plan de la fase `plan`). `PreToolUse` con matcher
`Write|Edit`, cruzando `tool_input.file_path` contra el write-set del `agent_id` que lo pide → **deny** si el archivo
es de otro agente.

Esto convierte tu regla más importante y más frágil en un invariante mecánico.

### 5.3 Run-DAG como `.canvas`

Los hooks `SubagentStart`/`SubagentStop` traen `agent_id` y `agent_type`. **El DAG sale solo.** Se materializa como
un archivo JSON Canvas 1.0 en el vault:

- nodos = fases, sub-agentes, gates; color por estado (verde ok / rojo blocked / amarillo warn)
- edges tipadas = `spawn`, `depends_on`, `retry_of`, `rollback`
- grupos = las fases

Obsidian lo abre y lo navegas. **Cero LOC de layout.**

---

## 6. Arquitectura

Un binario Go puro (`CGO_ENABLED=0`, `modernc.org/sqlite`) con cinco caras, y un plugin delgado que lo invoca.

```
loom init            # entrevista el repo -> genera loom.yaml (o migra desde un doc en prosa)
loom hook <event>    # lee el JSON del hook por stdin  (exec form; cero jq/curl/bash)
loom run <unit>      # conduce la máquina de fases
loom runs | show     # consulta
loom canvas <run>    # emite el .canvas al vault
loom budget | doctor # governor + diagnóstico
loom mcp             # servidor MCP (query del grafo de corridas)
loom tui             # visor Bubbletea            (FASE 3 — opcional)
loom serve           # sink HTTP de hooks + OTLP  (FASE 3 — opcional)
```

### El plugin (delgado, self-contained)

```
.claude-plugin/plugin.json        # name, version, license
.claude-plugin/marketplace.json   # en la raíz del repo
bin/loom[.exe]                    # <-- auto-PATH. Engram no hace esto; es su error.
.mcp.json                         # { "command": "${CLAUDE_PLUGIN_ROOT}/bin/loom", "args": ["mcp"] }
hooks/hooks.json                  # exec form -> bin/loom hook <event>
commands/                         # /loom-init /loom-research /loom-plan /loom-build ...
skills/loom-engine/SKILL.md       # cómo leer loom.yaml y ejecutar una fase
skills/loom-verdict/SKILL.md      # el sobre + la regla de la evidencia
```

**Estado en `${CLAUDE_PLUGIN_DATA}`, jamás en `${CLAUDE_PLUGIN_ROOT}`** — el root se borra en cada update del plugin
(los docs lo dicen explícito: "treat it as ephemeral, do not write state here"). Este es un pie de bala clásico.

### Los hooks

| Evento | Qué hace loom |
|--------|---------------|
| `SessionStart` | inyecta el estado de la unidad activa (fase, veredicto, presupuesto gastado) |
| `PreToolUse` (`Bash`) | **verdict gate**: deny en `git commit` sin veredicto ok |
| `PreToolUse` (`Write\|Edit`) | **write-set guard**: deny si el archivo es de otro agente |
| `SubagentStart` | crea el nodo en el DAG (`agent_id`, `agent_type`) |
| `SubagentStop` | cierra el nodo, captura `last_assistant_message`, ingesta tokens/costo |
| `Stop` | cierra el run; recalcula presupuesto; re-emite el `.canvas` |
| `PostToolUse` | ingesta eventos al log append-only (replay) |

### El spine SQLite

En `${CLAUDE_PLUGIN_DATA}/loom.db`. Esquema del v2 §6.2, intacto — era correcto.
Setup de concurrencia **completo** (el v2 omitía la línea que más importa):

```go
db, _ := sql.Open("sqlite", path)      // driver "sqlite", no "sqlite3"
db.SetMaxOpenConns(1)                  // <-- SIN ESTO te das SQLITE_BUSY a ti mismo
// PRAGMA journal_mode=WAL; busy_timeout=5000; synchronous=NORMAL; foreign_keys=ON
```

`SetMaxOpenConns(1)` serializa las escrituras **dentro** del proceso; WAL + `busy_timeout` absorbe la contención
**entre** procesos (los hooks son procesos separados). Los dos, no uno.

---

## 7. Qué NO se construye

Tan importante como lo que sí. El v2 iba a construir tres cosas que no hacen falta:

| v2 iba a construir | Por qué no |
|--------------------|------------|
| **TUI de grafo Bubbletea + layout Sugiyama propio** (~semanas) | JSON Canvas + Obsidian lo renderiza gratis. Fase 3 opcional, por gusto, no por necesidad. |
| **obsidian-bridge (módulo Go)** | Son **archivos**. Se escriben directo. El CLI de Obsidian exige la app abierta → inútil headless. |
| **Memoria semántica** | engram. Nicho saturado (~89k★). Interoperar por MCP. |
| **Motor de fan-out** | Dynamic Workflows de Claude Code (GA, 16 concurrentes / 1000 totales). **Gobernar, no re-orquestar.** |
| **Grafo de código** | graphify (tree-sitter, 0 tokens de LLM, MIT). |
| **Compresión de contexto** | headroom, si se demuestra que ahorra (§9). |

---

## 8. Cómo encajan las tres piezas externas

### graphify — memoria estructural (adoptar, desacoplado)

MIT, tree-sitter, **0 tokens de LLM para código**. Se enchufa donde tu flujo **paga el impuesto de re-orientación**:
la Fase 2 dice literalmente *"entiende qué YA existe (Grep antes que Read)"* — eso es exactamente el gasto que el
grafo elimina.

```
antes:  grep -r "keyword" ml/ -l  →  Read  →  Read  →  Read   (~15-20k tokens)
ahora:  graphify query "qué ya existe para filtrado de PASTIS"  (~1-2k)
```

**Con reservas honestas**: el 70x es best-case de **un** benchmark. Reviews independientes: <100 archivos ≈ nada;
100-500 ≈ 6-15x; 500-5000 ≈ 30-49x. Su hook `PreToolUse` **ya se rompió** con Claude Code ≥2.1.117. Es pre-1.0
(v0.9.15, branch `v8`, 537 issues abiertos).

→ **Desacoplado**: `capabilities.graph.provider: graphify | none`. Si falta, cae a grep. **No usamos su hook**
(usamos el nuestro) ni su capa `lessons`/`reflect` (**pisa a engram** y es su parte más débil).

### obsidian-skills — la superficie humana (adoptar, dependencia blanda)

MIT, de Steph Ango (CEO de Obsidian). Cinco skills markdown puros. No aportan código: **enseñan los formatos**.
Con ellos, el agente autora `.md` con frontmatter tipado, `.base` (dashboards) y `.canvas` (el DAG) correctamente.

`loom` escribe los archivos directo al vault. Obsidian los renderiza. **No hay integración que mantener.**

Caveat que sí importa: los **rows renderizados de un `.base` no son legibles por scripts**. El `.base` es vista humana;
SQLite es canónico. Nunca scrapear output de Bases.

### headroom — compresión (opcional, e **instrumentado**)

Admitido tras corregir mi propio error (§1.4). Pero entra **medido, no por fe**:

- `capabilities.compression.provider: headroom`, `memory: false` (engram es dueño de lo episódico).
- Instalación: `headroom init claude` (hooks, `supervisor_kind: none`). **No** el modo MCP: ahí `headroom_compress`
  es una tool que el modelo llama **voluntariamente**, *después* de que el contenido ya entró al contexto y **ya se
  facturó**. Solo el proxy intercepta de verdad.
- **`measure: true`**: como `loom` ya lleva contabilidad en dólares por nodo, **medimos si ahorra en TU fan-out**
  en vez de creerle al README. Hay una duda abierta y real de si los hooks disparan para sub-agentes lanzados con la
  Agent tool — y tu flujo es fan-out puro. Si no ahorra, se apaga con una línea del yaml.

Expectativa calibrada: **15-25% en coding**, ~90ms de latencia por request, 200-500 tokens de overhead en passthrough,
y un modo de fallo **silencioso** (mal configurado no da error, simplemente no ahorra → revisar `/stats`).

---

## 9. Roadmap

| Fase | Qué | Criterio de éxito |
|------|-----|-------------------|
| **0** | `loom.yaml` + JSON Schema + `loom init --from <doc en prosa>` | El flujo de AgroSat migra a un yaml de ~80 líneas y el original queda obsoleto |
| **1 (MVP)** | Binario Go: `init`, `hook`, spine SQLite, **verdict gate**, **write-set guard** | Un `git commit` sin evidencia **es imposible**. Dos agentes no pueden pisarse un archivo. |
| **2** | `run` (máquina de fases) + `canvas` + budget governor + MCP | El `.canvas` dibuja el camino real con reintentos visibles; el governor corta al romper el tope en dólares |
| **3** | graphify + headroom cableados y **medidos**; export OTLP | Números reales de ahorro en TU fan-out, no del README |
| **4 (gated)** | TUI Bubbletea | Solo si el `.canvas` demuestra no bastar |

**Criterio de neutralidad (bloqueante para v1)**: el mismo binario corre en AgroSat y en un repo web TS no
relacionado, y **lo único que difiere es el `loom.yaml`**.

---

## 10. Riesgos

- **Sobre-declarar.** Si el `loom.yaml` intenta expresar todo, se vuelve un DSL y nadie lo escribe. Regla: **si no lo
  puede hacer cumplir un hook o ejecutar un comando, no va en el yaml.** La prosa que quede, que quede en prosa.
- **`loom init` mediocre = producto muerto.** Si migrar cuesta una tarde, nadie migra. Es la feature más importante
  de la Fase 0, no una utilidad.
- **Dependencias pre-1.0** (graphify v0.9.15, headroom v0.31): por eso son **capacidades opcionales que degradan**,
  no dependencias duras. El core no las conoce.
- **APIs de Claude Code en movimiento**: Dynamic Workflows es GA pero el scripting puede cambiar. Los hooks son la
  superficie estable — apoyarse ahí.
- **El gate como fricción.** Un verdict gate que bloquea mal es peor que no tenerlo. Necesita un escape explícito y
  auditado (`loom override --reason "..."`), que **queda en el log**. Sin escape, el usuario desinstala el plugin.

---

## 11. Fuentes (verificadas jul-2026)

**Claude Code** — [plugins-reference](https://code.claude.com/docs/en/plugins-reference) (`bin/`→PATH, `${CLAUDE_PLUGIN_DATA}`, schema de `plugin.json`/`marketplace.json`) · [hooks](https://code.claude.com/docs/en/hooks) (30 eventos, `permissionDecision: deny`, exec form, tipo `http`)

**Referencia de implementación** — [Gentleman-Programming/engram](https://github.com/Gentleman-Programming/engram) (`store.go`: `SetMaxOpenConns(1)` + 4 PRAGMA; `hooks.json`; distribución del binario)

**Obsidian** — [kepano/obsidian-skills](https://github.com/kepano/obsidian-skills) (MIT) · [JSON Canvas 1.0](https://jsoncanvas.org/spec/1.0/) · [Bases syntax](https://help.obsidian.md/bases/syntax) · [CLI](https://obsidian.md/help/cli) (exige app abierta)

**Grafo de código** — [Graphify-Labs/graphify](https://github.com/Graphify-Labs/graphify) (MIT, branch `v8`) · [review crítico](https://www.roborhythms.com/graphify-review/) (procedencia del 71x, hook roto)

**Compresión** — [headroomlabs-ai/headroom](https://github.com/headroomlabs-ai/headroom) (Apache-2.0) · [#951 CERRADO 2026-06-18](https://github.com/headroomlabs-ai/headroom/issues/951) · [docs MCP](https://headroom-docs.vercel.app/docs/mcp) (compresión manual, post-facturación) · [medición independiente](https://andrewpatterson.dev/posts/token-savings-rtk-headroom/) (96% cache-hit; el grueso del ahorro vino de otra herramienta)

**SQLite** — [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) v1.53.0 (Go puro, driver `"sqlite"`)

**Interno** — `ecosistema-agentico-2026.local.md` (v2, superado por este doc) · `prompts_optimizers_v2.local.md` (el flujo que se generaliza; se vuelve el primer `loom.yaml`)
