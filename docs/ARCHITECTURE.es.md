# Arquitectura

> [English](ARCHITECTURE.md) · **Español**

Cómo está armado batten, y por qué cada pieza está donde está. Esta es la puerta de entrada al
código: si vas a cambiar algo, empezá acá y vas a saber cuál de los dieciséis paquetes lo posee.

Para qué *hace* batten, el [README](../README.md). Para instalarlo,
[INSTALL](INSTALL.es.md). Este documento asume los dos.

---

## La tesis, porque decide todas las fronteras de abajo

**Una regla que un documento solo puede pedir, un hook puede imponerla.**

Todo equipo serio escribe su proceso —fases, gates de revisión, "dos agentes nunca tocan el mismo
archivo", "nunca aprobar sin evidencia"— y después un agente lo ignora y *no pasa nada*. Ni error,
ni negativa. La regla era una frase, y una frase no puede detener un `git commit`.

batten convierte ese documento en **dato** y lo impone con hooks de Claude Code. Esa sola frase
explica toda la forma:

| la cosa | qué es | dónde vive |
|---|---|---|
| **el spec** | tu proceso, declarado como dato | `batten.yaml` en tu repo, parseado por `internal/spec` |
| **la imposición** | un binario que contesta payloads de hook por stdin | `cmd/batten` + `internal/hooks` |
| **la entrega** | el plugin de Claude Code: hooks, comandos, skills, MCP | `plugin/claude-code/` |
| **el registro** | qué pasó realmente | SQLite, `internal/store` |

**batten no orquesta.** Nunca lanza un agente, nunca rutea un modelo, nunca corre tu loop. Es una
decisión de diseño, no un faltante, y es la razón por la que `models.tiers` y el bloque
`resources:` fueron *borrados* del spec en vez de implementados: prometían un orquestador que no
existe. Todo lo que exigiría que batten corra tu flujo queda fuera de alcance por construcción.

---

## Las dos denegaciones

Todo lo demás las sirve. Si estás leyendo el código por primera vez, leé estos dos caminos.

### 1. El verdict gate — `internal/hooks/hooks.go`, `verdictGate`

Un `git commit` se deniega si el work item no tiene un sobre de veredicto citando evidencia. Donde
el gate declara `checks:`, hacen falta **dos** veredictos de **dos productores distintos**: uno que
batten generó *corriendo* los checks (`batten check`), y otro que escribió un revisor juzgando el
trabajo contra sus criterios de aceptación. Ninguno reemplaza al otro.

El orden de decisión importa y es fácil de equivocar:

```
destructionGuard      (regla 1 desatendida — primero, porque su error es irreversible)
  → bashWriteGuard    (un comando de shell escribiendo un archivo que no le pertenece)
    → verdictGate     (el commit en sí)
      → budget        (una corrida que reventó su techo no debería aterrizar en silencio)
```

`commitTarget` contesta "¿para qué unit es este commit?", y **el mensaje del commit le gana a la
atadura de sesión**. En trunk-based la rama no nombra nada, así que el mensaje es la única señal
que hay. El gate y el camino de cierre pasan los dos por esa función — antes la contestaban
distinto, y ese desacuerdo fue un defecto real.

### 2. El write-set guard — `internal/hooks/hooks.go`, `writeSetGuard`

Correr cuatro agentes a la vez solo es seguro si no pueden caer sobre el mismo archivo. "No lo van
a hacer porque el plan lo dice" es una esperanza; el guard es lo que la vuelve una propiedad, y esa
propiedad es lo que te deja hacer fan-out.

Tiene tres alcances, y el tercero existe porque los dos primeros tienen techo:

| alcance | mecanismo | límite |
|---|---|---|
| `Write`/`Edit`/`NotebookEdit` | match exacto de ruta contra el write-set reclamado | ninguno |
| `Bash` | parsea el comando (`internal/hooks/bashwrite.go`) | **no ve adentro de un heredoc, un target de Makefile ni un `go run`** |
| después del hecho | `batten scan-diff` le pregunta a git qué cambió | necesita ancla |

Ese límite del medio está **declarado, no escondido**: ningún parser de shell llega adentro de un
heredoc de python, y meter heurísticas más hondas en el camino crítico es lo que este proyecto
aprendió a no hacer sin medición. `scan-diff` es el complemento estructural — a git no se lo
engaña con un heredoc.

---

## Los hooks

`plugin/claude-code/hooks/hooks.json` declara **8 entradas** sobre **6 eventos**. Siete invocan el
binario; la octava es el bootstrap, en forma shell porque tiene que correr cuando el binario
todavía no existe.

| evento | qué decide |
|---|---|
| `SessionStart` | instala el binario (bootstrap) y después inyecta el briefing de fase: qué lee, si hace fan-out, qué exige su gate, el alcance del diff, y **si el gate está gobernando algo** |
| `PreToolUse` | las dos denegaciones, en el orden de arriba |
| `PostToolUse` | registra lo que pasó; cierra una corrida cuando aterriza su commit |
| `SubagentStart` / `SubagentStop` | los nodos y aristas del run graph |
| `Stop` | contabilidad de fin de turno |

**Lo único que hay que saber para leer la salida de un hook:** el silencio **no** es prueba de
ALLOW. `batten hook` no imprime nada y sale 0 por al menos seis razones distintas — allow, no
encontró spec, falla del store, stdin malformado, un panic recuperado, un evento desconocido. Todo
test de este repositorio que afirma que algo pasó lleva un **control positivo**: el mismo payload
con un campo cambiado que vuelve obligatoria la denegación. Si el control también sale mudo, el
hook nunca se activó y el "PASS" no probó nada.

---

## Los paquetes

Dieciséis bajo `internal/`, más `cmd/batten`. Cada uno sostiene una regla.

| paquete | posee | la regla que sostiene |
|---|---|---|
| `hooks` | las dos denegaciones, los briefings, el sobre | fallar abierto, pero nunca en silencio |
| `store` | SQLite: runs, verdicts, write-sets, events, criteria, scans | las migraciones son **expand-only** |
| `spec` | `batten.yaml`: parsear, validar, rechazar | un spec que permite aprobar sin evidencia no carga |
| `mcp` | el servidor MCP que consulta el modelo | contestar por UNA fase, no por el documento entero |
| `gitx` | todo shell-out a `git` | una falla devuelve error, nunca una lista vacía |
| `scan` | leer un repo que nunca vio, para proponer un spec | leer el backlog antes que los nombres de rama |
| `plan` | resolver `unit.plan` + `unit.locator` en criterios de aceptación | los criterios son dato, no prosa |
| `canvas` | el run graph como JSON Canvas y HTML autocontenido | nunca inventar una arista para la que el schema no tiene relación |
| `usage` / `statusline` | el ledger de tokens y el techo de cuota rodante | una cantidad no medible se reporta como no disponible, nunca como 0 |
| `vault` / `export` | salida a Obsidian | honrar la lista de export declarada, nada más |
| `install` | qué se envía y si la máquina que lo recibe puede correrlo | el binario tiene que aterrizar donde los hooks miran |
| `discovery` | encontrar skills, agentes y reglas que el repo ya tiene | complementar el proceso que existe; nunca pisarlo |
| `tui` / `render` | superficies de terminal | el mismo número se renderiza igual en todos lados |

`cmd/batten` es el CLI: 28 subcomandos sobre 29 brazos del `switch` (`version` tiene tres grafías).
Es deliberadamente sin cobra — cero dependencias de CLI. Con 2600+ líneas es el archivo más grande
del repo y un candidato conocido a refactor; no es urgente, porque partirlo en un archivo por
comando es todo el riesgo y ninguna evidencia observable.

---

## El registro

Una base SQLite, en `~/.batten/batten.db` salvo que `BATTEN_DB` diga otra cosa.

Esa ruta es deliberada y costó dos bugs aprenderla. El estado **no** vive en
`${CLAUDE_PLUGIN_DATA}`: los procesos de hook tienen esa variable y tu terminal no, así que una
ruta que dependa del entorno parte el estado en dos bases — la TUI dice "no hay runs" mientras los
hooks escriben runs felizmente en otro lado. Y `${CLAUDE_PLUGIN_ROOT}` queda prohibido en
cualquier caso: se borra en cada actualización del plugin.

Las tablas que cargan decisiones:

- **`runs`** — una por work item por intento. Lleva el ancla (`base_sha`), el worktree, el modo de
  enforcement y la marca de corrida desatendida.
- **`verdicts`** — con `source` (`agent` vs `batten`) y `target_digest`, la huella del árbol sobre
  el que se hizo el veredicto. Esa segunda columna es lo que hace que "verificado" signifique
  verificado contra *este* árbol y no contra el que existía cuando corrieron los checks.
- **`writesets`** — quién reclamó qué. Nunca se borra, y eso es lo que hace computable la métrica
  de sobre-declaración.
- **`events`** — el log de replay, escrito **después** del dispatch, con la decisión, la razón y la
  regla. Antes registraba el payload entrante y nada de lo que batten *hacía*, así que "¿cuántos
  commits denegaste esta semana?" no tenía respuesta.
- **`scans`** — una fila por `scan-diff`, para que el contraste de write-sets sea una historia y no
  una línea de stdout.

Las migraciones son expand-only: agregar columna, agregar tabla, nunca sacar. Dos binarios de
batten comparten una base rutinariamente —el que instaló el plugin y el que esté en tu PATH— así
que una migración que borrara algo dejaría al binario viejo leyendo un archivo que el nuevo ya
cambió, adentro de un hook, donde una falla es indistinguible del silencio.

---

## Los guards que mantienen el código honesto

Esta es la parte que la mayoría de los codebases no tiene, y conviene entenderla antes de agregar
una feature: varios de estos la van a rechazar.

| guard | qué impone |
|---|---|
| `TestEveryDeclaredFieldHasAConsumerOrIsDeclaredFuture` | todo campo de `batten.yaml` tiene un consumidor en producción, o una entrada explícita con su razón. **La lista está vacía** — ningún campo declarado sin lector. |
| `TestEveryMigrationIsAdditive` | las migraciones nunca borran, renombran ni eliminan |
| `TestEveryUnattendedRuleIsMechanicalOrRegisteredAsProse` | toda regla absoluta de `/batten-night` tiene un mecanismo cuyo identificador se *usa*, no solo se declara |
| `TestEveryEdgeRelationReadHasAProducer` | toda `edges.rel` que una superficie lee tiene algo que la escribe |
| `TestTheSpecsThisRepoShipsHaveNoDeadKeys` | el schema publicado y los ejemplos enviados no pueden contradecirse |
| `TestNoPrivateProjectTokensAreTracked` | el repo privado contra el que se hizo el field test no está nombrado en ningún lado, ni adentro del grafo generado |
| `TestNoCommentPointsAtADocumentThatIsNotHere` | ningún archivo trackeado cita la sección de un documento que un clon no recibe |
| `TestEveryCommandRefusesToRunWithoutTheBinary` | el primer bloque bash de cada comando `/batten-*` chequea el binario antes |
| `TestEveryDocumentThatDrivesAVerdictNamesBattenCheck` | todo documento que lleva a un agente hasta `batten verdict` nombra también `batten check` |

Existen porque un field test encontró 52 defectos en un proyecto con la suite verde: **los tests
verificaban que el código hiciera lo que hace. Nada verificaba que el spec prometiera solo lo que
el código hace.** Cada guard convierte una clase recurrente de eso en una denegación.

---

## Distribución

El plugin se envía **sin** binario — inflan el repo y se quedan viejos. Un hook `SessionStart`
corre `scripts/bootstrap.sh` (o `bootstrap.ps1` en Windows sin Git Bash), que baja el binario
estático de la plataforma desde el GitHub Release a `${CLAUDE_PLUGIN_ROOT}/bin/batten`.

Esa ruta no es una preferencia. `hooks.json` la nombra siete veces, `.mcp.json` una, Claude Code
agrega ese directorio al PATH —que es lo que hace resolver el `batten` pelado de los comandos
`/batten-*`— y `batten doctor` inspecciona exactamente ese archivo para contestar "¿está corriendo
el gate?". Un binario en cualquier otro lado es un binario que nadie invoca.

**Una sola parte del bootstrap falla cerrado:** la verificación sha256 contra el `checksums.txt`
del release. Todo lo demás en ese script es best-effort, porque un bootstrap no puede romper una
sesión. Esto no: el archivo que se está chequeando está por ser ejecutado por siete hooks y un
servidor MCP, así que instalarlo sin verificar es ejecución remota de código por descarga. Un hash
equivocado, un `checksums.txt` inalcanzable y una máquina sin herramienta de sha256 son la misma
frase —nadie puede responder por estos bytes— y reciben la misma respuesta.

Igual sale 0. `hooks.json` despacha `bash bootstrap.sh || powershell bootstrap.ps1`, así que un
exit distinto de cero significa "no hay bash", no "la descarga salió mal".

---

## Por dónde empezar a leer

| si querés entender… | leé |
|---|---|
| el commit gate | `internal/hooks/hooks.go` → `verdictGate`, `gateShortfall`, `commitTarget` |
| la cerca de write-sets | `internal/hooks/hooks.go` → `writeSetGuard`, después `bashwrite.go` |
| qué puede declarar un spec | `internal/spec/spec.go`, después `batten.schema.json` |
| qué se registra y por qué | `internal/store/store.go` — los comentarios de las migraciones son la historia |
| qué se le envía a un usuario | `plugin/claude-code/` — hooks, comandos, skills, `.mcp.json` |
| si de verdad funciona | `scripts/matrix-replica.sh` (41 aserciones) y `scripts/matrix-demo.sh` (26) |

Los mensajes de commit de este repositorio son largos a propósito. Donde un comentario dice *por
qué*, el commit que lo introdujo suele decir *cuánto costó averiguarlo*.
