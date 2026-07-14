# batten — dónde estamos y a dónde vamos

> Documento de estado. Actualizado: **2026-07-14**, tras el MVP (commit `1c1be26`) y su auditoría.
> El plan de ejecución detallado vive en [PLAN.md](PLAN.md); la tesis y las decisiones de diseño en [DESIGN.md](DESIGN.md).

---

## Qué es batten (la tesis en un párrafo)

Un agente de coding tiene tres memorias: **estructural** (qué ES el código → graphify), **episódica** (qué DECIDIMOS → engram) y **procedural** (CÓMO TRABAJAMOS → **nadie la tiene; ese es el hueco**). batten convierte el proceso de trabajo —que hoy vive en prosa que el agente puede ignorar— en un archivo declarativo (`batten.yaml`) que un binario Go **hace cumplir** con hooks de Claude Code. La regla que gobierna todo el diseño: *si un hook no lo puede hacer cumplir o un comando no lo puede ejecutar, no va en el yaml*.

---

## Dónde estamos

### ✅ Construido y VERIFICADO (no aspiracional — probado en sandbox)

| Qué | Evidencia |
|---|---|
| **Verdict gate** | `git commit` sin veredicto → `DENY`; con `blocked` → `DENY`; con `ok`+evidencia → `ALLOW`. El sobre con `evidence[]` vacío es rechazado por el binario antes de tocar la DB |
| **Write-set guard** | Agente B escribiendo archivo del agente A → `DENY` con su write-set listado; su propio archivo → `ALLOW` |
| **Contabilidad con subagentes** | 21k tokens contados = padre 6k (duplicado descartado) + subagente 15k (archivo separado). Un parser ingenuo pierde el 71% — verificado contra un transcript real de 1.9MB |
| **Presupuesto honesto** | Tres techos (tokens / USD imputado / % de cuota); un techo no medible reporta `NOT MEASURABLE`, jamás un cero inventado |
| **Sensor de cuota (statusline)** | Guarda `rate_limits` en SQLite; sin `rate_limits` NO fabrica una fila de 0% |
| **Run DAG → JSON Canvas** | `.canvas` válido contra el spec 1.0, colocado junto a la nota del vault con su embed |
| **Neutralidad** (criterio bloqueante) | El mismo binario corrió en AgroSat (7 dominios, H100, MLflow) y en una webapp TS (`TCK`, frontend/api) — solo cambió el `batten.yaml` |
| **Latencia de hooks** | Fast-path 17ms mediana (presupuesto: <50ms) |
| **Suite** | ~9.9k LOC Go; build + vet limpios; tests verdes en usage/statusline/mcp/vault/discovery |

### ⚠️ Construido pero NO verificado en condiciones reales

- **El plugin cargado de verdad en Claude Code** — todos los hooks se probaron inyectando JSON a mano, nunca disparados por el harness.
- **`agent_id` en `PreToolUse` real** — TODO el write-set guard cuelga de ese campo; si no llega, el guard falla-abierto en silencio. *La suposición más cargada del producto.*
- **Resolución del `.exe` en Windows** — `${CLAUDE_PLUGIN_ROOT}/bin/batten` sin extensión puede no arrancar.
- **Handshake MCP con cliente real** — la lógica de las tools pasa 13 tests; el stdio nunca habló con un cliente.
- **TUI interactiva** — compilada, nunca abierta en un terminal real.

### ❌ Parcial o ausente (la auditoría honesta)

| Pieza | Estado | Realidad |
|---|---|---|
| **Instalación en proyecto existente** | ausente | `batten init` es un stub; el caso de uso #1 no tiene flujo |
| Obsidian | 70% | Funciona pero **solo manual** (`batten canvas`); el vault no se llena solo |
| graphify | 30% | Solo prompts que el agente puede ignorar; sin staleness check ni detección |
| headroom | 10% | `measure: true` es un campo que **nada implementa** |
| engram | regresión | Los commands generalizados **perdieron** los `mem_search`/`mem_save` del flujo original |
| Distribución | rota | `bin/` vacío en git: un install de marketplace no tendría binario (el error exacto de engram que criticamos) |
| **Multi-sesión** | ausente | Dos Claude Code en paralelo: la atribución de unidad colapsa, los write-sets no se defienden entre runs, y nadie le dice a cada sesión cuál es su unidad |

---

## A dónde queremos llegar

**La prueba de fuego**: instalar batten en un proyecto que ya está en desarrollo debe ser esto —

```
1. /plugin marketplace add <repo>       # una vez por máquina
2. /plugin install batten@batten        # el binario llega solo
3. /batten-init                          # entrevista el repo; propone batten.yaml (o migra tu doc en prosa)
4. batten statusline --install           # opcional: habilita el techo de cuota
5. batten doctor                         # verde → trabajando
```

— y que entre **sin romper el sprint**: los gates arrancan en `enforcement: report` (advierten, no bloquean) y se endurecen a `enforce` cuando el equipo confía.

**El estado final por pieza:**

- **Obsidian**: el vault se llena **solo** (hook `Stop` + al guardar veredicto) — nota por run con frontmatter, dashboards `.base`, canvas embebido. Con graphify exportando al mismo vault: las tres memorias en un solo graph view.
- **graphify**: `doctor` avisa cuando el grafo está viejo; `init` lo detecta y lo propone; la fase plan lo consulta en vez de grepear (y degrada a grep sin drama).
- **headroom**: `measure` implementado de verdad — batten detecta el proxy, etiqueta cada run, y `batten measure` compara tokens con/sin, diciendo "datos insuficientes" cuando n<3 en vez de inventar conclusiones.
- **engram**: los touchpoints (`mem_search` en research/verify, `mem_save` al cerrar) de vuelta en los commands, condicionados a `capabilities.memory.provider`.
- **Multi-sesión**: cada sesión ligada a *su* unidad (binding sesión↔run); write-sets defendidos **entre** runs abiertos ("este archivo lo trabaja US-034 en otra sesión"); ambigüedad visible, nunca silenciosa; worktrees como patrón recomendado.
- **Distribución**: GoReleaser + bootstrap que descarga el binario a `${CLAUDE_PLUGIN_DATA}`; el plugin instalado desde marketplace funciona sin pasos manuales.
- **Confianza**: las 5 incógnitas del spike E0 respondidas con evidencia y documentadas en `docs/VERIFICATION.md`.

**Los principios que no se negocian en el camino:**

1. **Nunca inventar un número.** Lo no medible se reporta como no medible.
2. **Degradar, jamás romper.** Un hook sin `batten.yaml`, sin binario o con input malformado no-opea en silencio; nunca tumba la sesión.
3. **Fallar-abierto solo con aviso.** Si el gate no puede atribuir, no bloquea — pero lo dice.
4. **El escape existe y queda en el log.** `batten override --reason` — un gate sin escape se desinstala; un escape sin registro desarma el gate.

## El camino

Las fases están detalladas en [PLAN.md](PLAN.md):

**E0** spike de validación (plugin real, `.exe`, `agent_id`, MCP, TUI) → **P1** instalación + rampa de adopción → **P2** integraciones (vault auto, engram, graphify, headroom) → **P2.5** multi-sesión → **P3** distribución → **P4** verificación end-to-end.

E0 va primero a propósito: sus tres incógnitas pueden invalidar diseño, y media sesión de dogfooding vale más que otra ronda de features sobre suposiciones.
