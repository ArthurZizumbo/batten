# batten — el plugin al momento

> Descripción del estado real del plugin a **2026-07-28**, commit `24d7cd2`, versión `0.1.0`,
> sin release taggeado. Todo lo que sigue está verificado contra el código, no contra la
> intención. Donde algo se declara pero nadie lo lee, lo digo.

---

## 1. Qué es

batten es **memoria procedimental como datos**. Las otras dos memorias de un agente de código ya
estaban resueltas —la **estructural** (qué *es* el código: graphify) y la **episódica** (qué
*decidimos*: engram)—. La tercera, **cómo trabajamos**, no la tenía nadie: vivía en prosa dentro
de un `AGENTS.md` que el modelo puede leer y también ignorar.

La tesis del plugin cabe en una línea:

> Una regla que un documento solo puede *pedir*, un hook de `PreToolUse` puede **imponer**.

Concretamente, batten convierte dos reglas de proceso en dos negaciones mecánicas:

1. **El verdict gate** — un `git commit` sin evidencia citada se deniega.
2. **El write-set guard** — un subagente que escribe el archivo de otro subagente se deniega.

Todo lo demás del plugin (el grafo de la corrida, el libro mayor de tokens, la TUI, el canvas)
existe para que esas dos negaciones sean **auditables** y no sospechosas.

### Qué NO es

- **No re-orquesta al agente.** Los Dynamic Workflows de Claude Code ya corren el fan-out.
  batten *gobierna* el fan-out: los rieles, no el motor.
- **No comprime contexto.** Si headroom ahorra tokens en tu fan-out, úsalo — y como batten ya
  cuenta tokens por nodo, podés *verificar* si de verdad ahorra.
- **No guarda memoria episódica.** Eso es engram. batten guarda proceso.
- **No inventa tu workflow.** Las fases, los nombres y los dominios salen de tu `batten.yaml`.

---

## 2. Arquitectura en una pantalla

```
                     ┌──────────────────────────────────────┐
   batten.yaml ─────▶│  spec (internal/spec)                │
   (tu proceso,      │  valida, resuelve fases y gates      │
    declarado)       └────────────────┬─────────────────────┘
                                      │
   Claude Code ──hook JSON (stdin)──▶ │
                                      ▼
                     ┌──────────────────────────────────────┐
                     │  hooks (internal/hooks)              │
                     │  ► verdictGate    ► writeSetGuard    │──▶ deny / advise / silencio
                     └────────────────┬─────────────────────┘
                                      │
                                      ▼
                     ┌──────────────────────────────────────┐
                     │  store (internal/store) — SQLite      │
                     │  runs · nodes · edges · writesets     │
                     │  verdicts · usage · quota · overrides │  ◀── CANÓNICO
                     └────────────────┬─────────────────────┘
                                      │
        ┌──────────────┬──────────────┼──────────────┬────────────────┐
        ▼              ▼              ▼              ▼                ▼
     CLI/TUI        canvas         vault          MCP           statusline
   (leer runs)   (.canvas JSON)  (Obsidian)   (el agente      (único sensor
                                               consulta)       de cuota)
```

**SQLite es canónico.** El `.canvas`, la nota de Obsidian y cualquier export son proyecciones
con pérdida de lo que vive ahí. Esa decisión es la que permite que dos sesiones y varios
procesos de hook escriban el mismo archivo sin pisarse (WAL + `busy_timeout` + una sola
conexión de escritura por proceso).

---

## 3. Instalación y componentes

| componente | ruta | qué hace |
|---|---|---|
| binario | `plugin/claude-code/bin/batten[.exe]` | todo; los hooks lo invocan en forma exec |
| bootstrap | `scripts/bootstrap.sh` | descarga el binario del release si falta |
| hooks | `hooks/hooks.json` | registra los 7 eventos |
| comandos | `commands/*.md` | los 6 slash-commands |
| skills | `skills/*/SKILL.md` | `batten-engine`, `batten-verdict` |
| MCP | `.mcp.json` | expone el grafo de la corrida al agente |

**Los hooks son forma exec, no shell.** El binario lee el JSON del hook por stdin y escribe la
decisión por stdout. Sin `jq`, sin `curl`, sin `bash` — que es exactamente lo que hace frágiles
en Windows a los hooks que sí lo usan.

`.gitattributes` no es estilo sino corrección: `bootstrap.sh` commiteado con CRLF da
`#!/usr/bin/env bash\r` → *bad interpreter* en todo Linux/macOS, el binario nunca se descarga y
los hooks no-opean **en silencio**.

---

## 4. Los siete hooks

| evento | matcher | qué hace batten |
|---|---|---|
| `SessionStart` | `startup\|resume\|clear\|compact` | bootstrap del binario + banner de estado: qué unit está abierto, si hay veredicto, si la sesión está atada a un unit, y **si el gate no está gobernando nada** |
| `PreToolUse` | `Bash` | **verdict gate** sobre `git commit` + evaluación de presupuesto |
| `PreToolUse` | `Write\|Edit\|NotebookEdit` | **write-set guard** |
| `PostToolUse` | `Bash` | ata la sesión al unit tras `batten phase`; cierra el run tras un commit exitoso |
| `SubagentStart` | (todos) | crea el nodo del subagente y la arista `spawn` desde la fase |
| `SubagentStop` | (todos) | cierra el nodo con `ok`/`failed` según su mensaje final |
| `Stop` | (todos) | exporta la nota de vault y el canvas |

### El silencio de `batten hook` NO es prueba de ALLOW

Esto es la trampa central para cualquiera que pruebe el plugin. `batten hook` no imprime nada y
sale 0 por **al menos seis razones distintas**:

1. allow genuino
2. no encontró `batten.yaml`
3. falló el store
4. stdin malformado
5. un panic recuperado
6. un evento desconocido

Toda afirmación de "esto pasó el gate" necesita un **control positivo**: el mismo payload con un
campo cambiado para que la negación sea obligatoria. Si el control también sale en silencio, el
hook nunca se enganchó y el PASS no prueba nada.

---

## 5. El CLI completo

`batten <subcomando>`. Las banderas están listadas como las acepta el parser real.

### Arranque y salud

| comando | banderas | qué hace |
|---|---|---|
| `batten init` | `--from <doc>`, `--scan-json` | escanea el repo y escribe `batten.yaml` en modo report |
| `batten doctor` | — | valida el spec y reporta capacidades vivas. **Sale 1** si el spec es inválido |
| `batten version` | `--version`, `-v` | imprime la versión |

`init` deriva el unit **del backlog primero** y de los nombres de rama después: un repo planeado
pero no trabajado no tiene ramas feature, y el backlog es donde los ids realmente viven. Necesita
**≥3 encabezados** `### US-0NN` para llamarlo convención; con menos cae a `TASK-\d+` a propósito.

`--from` **hoy solo imprime su propio argumento de vuelta** — no lee el documento ni lo registra
como `unit.plan`, y acepta rutas inexistentes con exit 0. Está en el plan de mejora.

### Ciclo de vida de un unit

| comando | banderas | qué hace |
|---|---|---|
| `batten phase <unit> <fase>` | — | abre o avanza el run; graba el **ancla** (SHA base) |
| `batten claim <agent-id> <file>...` | — | declara el write-set de un subagente |
| `batten verdict` | `--unit`, `--file v.json` | graba el veredicto del **revisor** |
| `batten check <unit>` | `--gate <g>` | **CORRE** los checks del gate y graba lo que imprimieron. **Sale 1** si queda BLOCKED |
| `batten close <unit>` | `--status ok\|failed` | cierra por el gate y libera los write-sets |
| `batten override <unit>` | `--reason "..."` | escape auditado del gate |

### Lectura

| comando | banderas | qué hace |
|---|---|---|
| `batten runs` | — | lista los runs |
| `batten show <unit>` | — | detalle: fases, fan-out, **ambos veredictos** |
| `batten canvas <unit>` | `--out <ruta>` | emite el DAG como JSON Canvas |
| `batten budget [<unit>]` | — | estado del gobernador: tokens / $ imputado / cuota |
| `batten measure` | — | gasto por modelo y qué compraron las capacidades |
| `batten tui` | — | revisar runs en una UI de terminal |

### Integración

| comando | banderas | qué hace |
|---|---|---|
| `batten mcp` | — | servidor MCP (stdio) |
| `batten statusline` | `--install` | línea de estado + **único sensor de cuota** local |
| `batten ingest <unit>` | `--transcript <ruta>` | valoriza los tokens de un transcript |
| `batten hook <evento>` | `--tap` | entrada de hooks (JSON por stdin → JSON por stdout) |
| `batten hook-debug` | `--on`, `--off`, `--show`, `--chain` | inspección de payloads reales de hook |

### Variable de entorno crítica

**`BATTEN_DB`** — ruta de la base. Si no está puesta, `dbPath()` **cae a la base real del
usuario** (`~/.batten/batten.db`). Cualquier prueba en sandbox debe exportarla antes de *cada*
comando; olvidarla contamina el vault real.

---

## 6. El gate, exactamente como funciona

Esta es la parte que hay que entender bien porque es el producto.

### Dos veredictos, de dos productores distintos

Cuando el gate declara `checks:`, un commit necesita **ambos**:

| veredicto | quién lo escribe | qué prueba |
|---|---|---|
| `source: batten` | `batten check` | que los checks declarados **se corrieron** |
| `source: agent` (o cualquier otro) | `batten verdict` | que alguien **juzgó** el trabajo contra sus criterios de aceptación |

Ninguno sustituye al otro. `batten check` por sí solo **no cierra un unit** — probar que la suite
pasa no dice nada sobre si el trabajo cumple lo que se pidió.

### El orden de decisión en `verdictGate`

```
¿el comando es un git commit?          no → silencio
¿el spec gatea el cierre?              no → silencio
¿el mensaje del commit nombra un unit? sí → ese unit manda sobre la atadura de sesión
¿se pudo atribuir el commit?           no → AVISO (no denegación): dice qué no está gateado
¿hay run abierto para ese unit?        no → AVISO: "este commit NO está gateado"
¿hay override registrado?              sí → pasa, y queda en el registro
¿hay veredicto?                        no → DENY
¿evidencia vacía con result=ok?        sí → DENY
¿el result es el requerido?            no → DENY
¿el gate declara checks?
   sí → ¿falta el pase de batten o el del revisor? → DENY
   no → guarda un AVISO y sigue (nunca retorna acá)
¿el presupuesto está excedido y on_exceed=block? → DENY
                                        → devuelve el aviso pendiente, si lo hay
```

Dos cosas que ese orden codifica y que costaron bugs reales:

- **Un aviso nunca puede tapar una denegación.** La rama del gate sin checks *guarda* su aviso y
  sigue; si retornara ahí, el presupuesto dejaría de aplicarse.
- **Fallar-abierto es válido; fallar-abierto en silencio no.** Cuando batten no puede atribuir un
  commit, no deniega —negar lo que no puede verificar haría que lo desinstalen el día uno— pero
  **lo dice**.

### `enforcement: report` vs `enforce`

`report` convierte cada denegación en un aviso visible. Es la rampa de adopción: un equipo a
mitad de sprint no puede quedar bloqueado el día uno. `init` arranca siempre en `report`.

---

## 7. El write-set guard

Un fan-out corriendo: cuatro subagentes, write-sets disjuntos, cada uno declarado con
`batten claim` al lanzarse. La tabla `writesets` tiene `PRIMARY KEY (run_id, path)` — **un dueño
por archivo, impuesto por la base**, no por disciplina.

**La asimetría es deliberada:** con `agent_id` sabemos exactamente quién escribe, y una colisión
es DENY duro. **Sin** `agent_id` no podemos distinguir al dueño legítimo del intruso, así que es
un aviso. Los riesgos no son simétricos: un aviso ruidoso sobre una invasión real es recuperable;
bloquear en silencio toda escritura legítima brickearía el fan-out entero.

Huecos conocidos (§ plan de mejora): un `claim` de directorio se acepta y no cerca nada; una
escritura por `Bash` (`>`, `sed -i`, heredoc) no pasa por el guard.

---

## 8. El presupuesto — tres techos honestos

En una suscripción el costo marginal de un token es **cero**, así que "dólares gastados" es el
techo equivocado. batten declara tres:

| techo | qué es | se puede medir? |
|---|---|---|
| `tokens_per_run` | lo único que se puede contar exacto | sí, si se ingirió un transcript |
| `imputed_usd_per_run` | lo que *habría* costado por API. No es una factura: es el valor que se está sacando del plan | sí, para modelos con precio publicado |
| `quota_pct_per_run` | parte de la ventana rodante de 5h. Anthropic no publica números absolutos, así que **el porcentaje es la única métrica de cuota confiable** | solo con `batten statusline` instalado |

`on_exceed`: `block` | `warn` | `downgrade_effort`.

### El principio #1

> **Nunca reportar un número que no se tiene.**

Un run del que nadie ingirió transcript **no gastó cero** — está *sin medir*, y esas dos
situaciones piden respuestas opuestas. batten distingue:

```
US-005  usage NOT MEASURED (not zero — nothing has been ingested for this run)
  · tokens   NOT MEASURABLE — no usage has been measured for this run —
             run `batten ingest <unit> --transcript <path>`
```

Y cuando la valla temporal descarta requests anteriores al run (correcto: un run no hereda 30
horas de sesión), lo **dice en voz alta**, con tokens y dólares, en vez de tragárselo.

---

## 9. El grafo de la corrida

| tabla | qué guarda |
|---|---|
| `runs` | un run por unit-intento: fase, estado, `base_sha`, tokens, USD imputado, cuota inicial |
| `nodes` | fases y subagentes. `node_id = <run_id>:p-<fase>` o `<run_id>:n-<agent_id>` |
| `edges` | **aristas tipadas**: `spawn` \| `depends_on` \| `retry_of` \| `supersedes` \| `rollback` |
| `writesets` | dueño por archivo |
| `verdicts` | append-only, con `source` |
| `usage` | una fila por (request, run), idempotente por `request_id` |
| `quota_snapshots` | muestreo de la ventana de 5h |
| `overrides` | escapes auditados |
| `events` | log de reproducción, append-only |

**Las aristas tipadas son la razón de que SQLite sea canónico y no un caché.** Una traza OTel
plana no puede expresar más que una arista de padre y links sin tipo; `retry_of` y `supersedes`
son exactamente lo que hace legible un fan-out con reintentos.

Los ids de nodo **llevan su run**. Un `p-build` global era literalmente una fila para toda la
base: el segundo unit que entrara a `build` se llevaba la fila del primero.

---

## 10. Las capacidades opcionales

```yaml
capabilities:
  graph:      { provider: graphify, query_before_read: true, lessons: false }
  memory:     { provider: engram }
  obsidian:   { vault: ~/vaults/acme, export: [runs, verdicts, canvas] }
  compression:{ provider: headroom, measure: true }
```

Todas **degradan con gracia**: sin proveedor de grafo, la fase de plan hace grep; sin vault, no
se escribe canvas; sin statusline, el techo de cuota se reporta como no medible y los otros dos
siguen mordiendo.

### Estado real de cada integración

| capacidad | qué hace HOY | dónde |
|---|---|---|
| **graphify** | `doctor` lo detecta en PATH, avisa si falta `graph.json`, y detecta el **merge driver** de `graph.json` (crítico: pesa ~1MB y está commiteado, dos ramas dan conflicto garantizado). `batten phase` marca el run con si el grafo estaba fresco, para que `measure` compare después | binario |
| | `god-nodes --json` y `affected "X"` los usa la **fase de plan**, en prosa, dentro de `/batten-plan` | comando |
| **engram** | `doctor` lo detecta. `/batten-plan` indica `mem_search` antes de planear; `/batten-verify` antes de juzgar; `/batten-close` escribe la decisión | comandos, **no el binario** |
| **obsidian** | `export.Run` escribe la nota del run + el canvas + los dashboards cuando `vault` está seteado. Dispara desde el hook `Stop`, el comando `canvas` y tras un veredicto | binario |
| **headroom** | solo se **mide**: `measure` compara runs con y sin la bandera. Admitido, no tomado en fe | binario |

> **La cadena graphify → engram para el subagente NO existe.** Ver §12 y el plan de mejora.

---

## 11. Los comandos y skills del plugin

| slash-command | qué hace |
|---|---|
| `/batten-init` | entrevista el repo (o un doc en prosa) y escribe `batten.yaml`; usa `init --scan-json` |
| `/batten-plan` | decide dominios, sub-tareas paralelas y sus **write-sets disjuntos** |
| `/batten-build` | el fan-out: un subagente por dominio y por sub-tarea, cercado a su write-set |
| `/batten-verify` | el gate: contrasta el diff con los criterios y emite veredicto con evidencia |
| `/batten-close` | provenance, artefacto de resolución, y el commit que el gate debe permitir |
| `/batten-night` | desatendido: build → verify → fix → re-verify, **parando antes del cierre** |

Los *nombres* de las fases salen de tu `batten.yaml`. Los comandos leen el spec y corren la fase
que coincida — no hardcodean un workflow.

`/batten-night` es el que hay que leer antes de confiar: nunca borra nada (si *quisiera*, lo dice
en el reporte de la mañana), nunca hace override del gate, y para antes del cierre. Los techos de
presupuesto son el disparador que a una corrida desatendida le falta — no hay humano despierto
para notar que lleva tres horas contra el mismo test rojo.

| skill | cuándo |
|---|---|
| `batten-engine` | correr una fase declarada en `batten.yaml` |
| `batten-verdict` | emitir el envelope que cierra una fase de gate |

---

## 12. Declarado pero no leído — inventario honesto

Un spec que declara algo que nadie consume es peor que no declararlo: el usuario lo escribe,
asume que gobierna, y no gobierna nada. Estado real:

### Impuesto por el binario (mecanismo real)

`gate` · `requires_verdict` · `anchor: git_sha` · `budget.*` · `enforcement` · `unit.pattern`
(para resolver el unit) · `domains.*.check` (vía `gates.checks`) · `capabilities.obsidian.vault`

### Pasado al agente por MCP `batten_spec` (una *declaración* que el modelo puede honrar)

`phases[].optional` · `phases[].interactive` · `phases[].fanout` · `phases[].reads` ·
`phases[].when` · `domains[].invariants` · `domains[].skills` · `resources`

### Declarado y leído por **nadie**

| campo | qué promete | consumidores |
|---|---|---|
| `phases[].diff_from: anchor` | operar solo sobre el diff del unit | **0** |
| `phases[].graph_query` | consultar el grafo en vez de grepear | **0** |
| `capabilities.graph.query_before_read` | preguntar al grafo antes de leer | **0** |
| `models.tiers` / `models.phases` | rutear subagentes por modelo, y **verificarlo** desde el ledger | **0** |
| `provenance.format` | qué ancla un unit cerrado a evidencia reproducible | **0** |
| `edges.rel = retry_of` | reintentos visibles en el grafo | 4 consumidores, **0 productores** |

Los tres primeros son exactamente lo que haría falta para la cadena graphify→engram que el
subagente debería seguir.

---

## 13. Estado de calidad

- **Suite verde** en 13 paquetes, CI en Linux/macOS/Windows.
- **Field test hecho**: 90 comportamientos confirmados funcionando, 80 hallazgos, los 63 sin
  verificar pasados por un verificador adversarial. 52 confirmados, 11 refutados.
  Ver [`../FIELD-TEST.md`](../FIELD-TEST.md).
- **8 defectos corregidos**, cada uno con un test que falla contra el commit anterior.
- **Sin release taggeado.** Nunca adoptado por un proyecto ajeno, con gente que no lo escribió.

El recorrido de adopción completo, con salida capturada de una corrida real incluido el control
negativo, está en [`../QUICKSTART.md`](../QUICKSTART.md).

---

## 14. Las decisiones que hay que conocer antes de tocar el código

1. **SQLite es canónico.** Todo lo demás es proyección con pérdida.
2. **Nunca reportar un número que no se tiene.** No medible se reporta no medible, jamás como 0.
3. **Fallar-abierto solo en voz alta.** Un gate que degrada en silencio a confiar en el modelo es
   *peor* que no tener gate, porque se le cree.
4. **Un aviso nunca gana a una denegación.**
5. **Dos veredictos de dos productores.** La máquina dice que corrió; el revisor dice que está bien.
6. **El binario hace el trabajo del hook.** Sin bash, sin jq — o Windows se rompe.
7. **Un id de nodo que no lleva su run no es un identificador.**
