# batten

> [English](README.md) · **Español**

> **El 78 % de las fallas de agentes son silenciosas.** No levantan un error — el agente reporta
> éxito y el trabajo está mal. En las mismas mediciones, **los gates determinísticos triplicaron la
> confiabilidad**, y lo hicieron negándose a cosas en vez de pedirle a un agente que tenga cuidado.
> ([arXiv:2607.07405](https://arxiv.org/html/2607.07405v1))
>
> **batten es uno de esos gates**, para el flujo que tu repo ya declara.

Miralo antes de configurar nada — arma un repo descartable, corre el flujo entero y lo borra. No
toca tu repositorio ni tu base de datos:

```console
$ batten demo
```

**Y no te bloquea por default.** `batten init` escribe `enforcement: report`: los gates miran y
avisan, no se rechaza nada, y vos lo pasás a `enforce` cuando confiás en lo que venís leyendo.
`batten report` es lo que se lee.

---

**Un flujo escrito en prosa no puede imponerse a sí mismo.**

Todo equipo serio escribe su proceso. Fases, gates de revisión, "dos agentes nunca tocan el mismo
archivo", "nunca aprobar sin evidencia". Va a un `CONTRIBUTING.md`, o a un archivo de prompt de 700
líneas, y se pega en cada sesión nueva.

Después un agente lo ignora, y **no pasa nada.** Ni error, ni negativa. La regla era una frase en un
documento, y una frase no puede detener un `git commit`.

Las dos reglas que más importan son exactamente las dos que fallan así:

> *"Nunca cerrar un work item con `result: ok` y `evidence[]` vacío."*
> *"Dos agentes nunca deben escribir el mismo archivo."*

Las dos son súplicas. Un agente distraído rompe la primera porque el código *se ve* bien, y te
enterás en producción. Rompe la segunda a la hora seis de un fan-out nocturno, y te enterás en el
merge.

batten convierte ese documento en **dato** —un `batten.yaml` que tu repo declara— y después **lo
impone con hooks de Claude Code.** Las reglas dejan de ser consejo y pasan a ser denegaciones.

---

## El producto son dos denegaciones

Todo lo demás en este repo existe para servirlas.

**Se les sumaron cinco más**, y están acá listadas en vez de enterradas porque cada una era prosa
pidiéndole a un modelo que se portara bien: mergear un worktree sin los dos veredictos, borrar algo
durante una corrida desatendida, `batten override` mientras nadie mira, commitear durante una
corrida desatendida *incluso con* los veredictos puestos, y pasarse del techo de iteraciones
declarado. Las últimas cuatro son las reglas absolutas de `/batten-night` — 112 líneas de markdown,
en el comando más peligroso que este plugin envía, en el único lugar donde un error es irreversible
y no hay nadie despierto para atajarlo. Fue el caso más ruidoso de batten no tomando su propia
medicina.

### 1. El verdict gate

Un agente termina un work item, se siente bien con él, y va por el commit:

```console
$ git commit -m "feat: add order rate limiting"

  PreToolUse:Bash [batten] permission denied

  batten: US-034 has no verdict envelope. Run the "qa" phase before committing.
  To proceed anyway (recorded in the audit log): batten override US-034 --reason "..."
```

Corre la fase de QA, e intenta aprobar sin nada a lo que apuntar:

```console
$ echo '{"check_id":"US-034-qa","result":"ok","evidence":[]}' | batten verdict --unit US-034

  batten: result "ok" with an empty evidence[] is not allowed: an approval must cite
  something (command output, test counts, a criterion verified). Without evidence the
  result is "blocked"
```

Rechazado por el binario, antes de llegar a la base de datos. Y si un veredicto entrara por otro
camino, el hook del commit lo atrapa de forma independiente.

**Esta es la falla que batten existe para matar: cerrar un work item porque *se ve* bien.**

Así que cita tres cosas reales. Sigue sin alcanzar, porque una cita también es solo texto:

```console
$ batten verdict --unit US-034 --file verdict.json
verdict recorded: US-034 qa=ok (3 evidence)

$ git commit -m "feat: add order rate limiting"

  batten: US-034 has no batten-verified pass. The gate's checks must be RUN, not asserted.
  Run: batten check US-034
```

Cuando el gate declara `checks:`, batten insiste en correrlos él mismo:

```console
$ batten check US-034
  ✓ go build ./...
  ✓ go vet ./...
  ✓ go test ./...

US-034: OK (batten-verified). all gate checks passed (batten ran them)

$ git commit -m "feat: add order rate limiting"
[feature/US-034 8f2a1c9] feat: add order rate limiting
```

**Dos veredictos, de dos productores distintos.** `batten check` prueba que los checks declarados
corrieron; el sobre prueba que alguien juzgó el trabajo contra sus criterios de aceptación. Ninguno
reemplaza al otro, y `batten check` por sí solo no cierra un unit.

**Y los criterios son dato, no prosa.** "Criterios de aceptación" es la frase alrededor de la cual
está escrito todo gate de revisión, y durante casi toda la vida de este proyecto existió solo en
oraciones: `evidence` era una lista plana de strings, y nada podía decir *cuál* criterio cubría una
evidencia dada — así que "3 evidence" te daba un número y no si el trabajo estaba hecho.

batten lee los criterios del documento donde ya los tenés. `unit.plan` nombra tu backlog y
`unit.locator` dice cómo se ve el encabezado de un work item (`### {id}`); entrar a una fase siembra
los criterios del item como filas. Un veredicto cubre un criterio **citándolo**:

```json
{ "check_id": "US-034-qa", "result": "ok",
  "evidence": ["AC-1: curl -i shows 429 on request 11",
               "AC-2: the Retry-After header names the window",
               "go test ./...: PASS (exit 0)"] }
```

Un prefijo de string sobre el formato que ya existía — no un objeto anidado nuevo, y es
deliberado: pasarle objetos a un campo que espera strings es uno de los hallazgos de la propia
lista de defectos de este repo. La evidencia sin citar sigue siendo evidencia; solo que no afirma
cubrir nada.

Lo que eso compra es un cuerpo de PR que hace una afirmación mucho más fuerte que una lista de
citas:

```markdown
### Acceptance criteria — 2 of 3 covered

| # | criterion | covered by |
|---|---|---|
| AC-1 | returns 429 over the limit | ✅ `curl -i shows 429 on request 11` |
| AC-2 | the header names the window | ✅ `the Retry-After header names the window` |
| AC-3 | the limit is per API key | — **not covered**: no approving evidence cites it |
```

La fila sin cubrir es el punto. Un tablero que muestra solo las verdes adula la corrida, y quien lo
lee no puede distinguir entre terminado y sin examinar. Solo un veredicto **que aprueba** cubre
algo — un veredicto `blocked` citando `AC-3` está describiendo lo que falló, y marcarlo como
cubierto invertiría su significado. Un work item sin criterios en el plan reporta *"no criteria
seeded"*, nunca `0/0`: una lista vacía no es una lista satisfecha.

### 2. El write-set guard — lo que hace seguro al fan-out paralelo

Este se lee como una restricción y es lo contrario. **Correr cuatro agentes a la vez solo es seguro
si no pueden caer sobre el mismo archivo**, y "no lo van a hacer porque el plan lo dice" no es una
propiedad — es una esperanza que aguanta hasta la hora seis. El guard es lo que la convierte en
propiedad, y esa propiedad es lo que te deja hacer fan-out.

Hay un fan-out corriendo. Cuatro subagentes, write-sets disjuntos, cada uno reclamado al lanzar con
`batten claim`. El agente `ml/A` —dueño del data loader— decide que también necesita arreglar el
trainer, que es de `ml/C`:

```console
  PreToolUse:Edit [batten] permission denied

  batten: write-set collision. ml/farslip/train.py belongs to another agent's write-set
  (node-7f3a/ml-C); you are node-2b91/ml-A.
  Two agents must never write the same file — that is what makes the fan-out safe.
  Your write-set:
    ml/data/pastis_filter.py
    tests/ml/data/test_pastis_filter.py
  If this file genuinely belongs to you, the plan is wrong: fix the plan, do not cross the fence.
```

No es un conflicto de merge descubierto a las 3 de la mañana. Es un `Edit` denegado, a los cinco
segundos, nombrando el archivo, el dueño, y qué hacer al respecto.

Y fijate en lo que el mensaje **se niega** a hacer: no ofrece una manera de pasar. **Si dos agentes
necesitan ese archivo, el plan estaba mal** — el arreglo es fusionar las sub-tareas o secuenciarlas,
no abrir la cerca. La cerca es la feature.

Un dueño por archivo es un `PRIMARY KEY` en SQLite, no un párrafo pidiéndole a los agentes que
tengan cuidado. Y como una ruta es solo un *nombre* para un archivo, el guard también le pregunta al
sistema operativo: dos nombres para un archivo en disco —un hard link, un directorio symlinkeado—
resuelven al mismo dueño.

---

## Qué va en `batten.yaml`

Tu proceso, como dato. El motor no sabe nada de tu proyecto — lee `check` y lo corre; no tiene idea
de qué es `pytest`.

```yaml
version: 1
project: acme

unit:                                  # el sustantivo de work item que YA usás
  name: US
  pattern: 'US-\d{3}'                  # aparece en ramas, prompts, commits
  plan: docs/backlog.md

artifacts:
  planning: docs/us-planning/{id}.md   # {id} es obligatorio — si no, cada unit
  resolved: docs/us-resolved/{id}.md   # pisaría el mismo archivo

phases:                                # la máquina de estados. Los nombres son tuyos.
  - id: plan
    graph_query: true                  # preguntarle al grafo de código "¿qué ya existe?"
  - id: build
    fanout: true                       # un subagente por dominio + por sub-tarea
    reads: [planning]
    anchor: git_sha                    # graba el SHA base: toda fase posterior
  - id: verify                         #   diffea desde ACÁ, nunca desde HEAD~N
    gate: qa
    diff_from: anchor
  - id: close
    requires_verdict: ok               # EL GATE DURO — un hook, no una convención

domains:                               # los ejes del fan-out
  backend:
    path: backend/
    rules: backend/AGENTS.md           # el agente lee esto PRIMERO
    check: ['make lint', 'make test']  # verbatim de tu Makefile
    coverage: 70
    agent: acme-backend-dev            # tu propio subagente custom, si tenés uno
    invariants:                        # viajan VERBATIM al prompt de cada agente
      - session_id en toda query
      - la lógica va en el service, nunca en el router

gates:
  qa:
    checks: ['make check', 'make test']
    verdict: required
    evidence: required                 # evidence[] vacío -> blocked. No negociable.

budget:
  tokens_per_run: 3_000_000
  imputed_usd_per_run: 8.00
  quota_pct_per_run: 15
  max_iterations: 3
  on_exceed: block
```

Los **invariants** son las líneas de más valor del archivo. Son las reglas que un revisor atajaría y
un agente distraído rompería, y se copian carácter por carácter al prompt de cada agente del
fan-out.

La regla que gobierna qué pertenece acá:

> **Si un hook no puede imponerlo o un comando no puede correrlo, no va en `batten.yaml`.**

La prosa que sobrevive esa prueba se vuelve dato. La que no —"pensá bien la arquitectura"— sigue
siendo prosa, y está bien. Sobre-declarar convierte el spec en un DSL que nadie mantiene.

Hay un JSON Schema en [`batten.schema.json`](batten.schema.json), así tu editor completa y valida
el archivo mientras escribís.

### Sobre el presupuesto

Con una suscripción, el costo marginal de un token es **cero** — así que "dólares gastados" es el
techo equivocado. Tres honestos lo reemplazan:

| techo | qué es |
|---|---|
| `tokens_per_run` | Exacto. Lo único que se puede contar con certeza — y contado sobre el padre **y cada subagente**, porque viven en transcripts separados y contar solo el padre subcuenta feo un fan-out. |
| `imputed_usd_per_run` | Lo que esos tokens *habrían* costado por API. **No es una factura** — es una medida del valor que le estás sacando a la suscripción. |
| `quota_pct_per_run` | Porción de la ventana rodante de 5 horas. Anthropic no publica números absolutos de cuota, así que **un porcentaje es la única métrica de cuota confiable.** |

```console
$ batten budget
US-034  1.4M tokens, $3.40 imputed
  · tokens       1.4M / 3.0M  [=====.......]
  · imputed_usd  $3.40 / $8.00  [=====.......]
  · quota_pct    NOT MEASURABLE — install the statusline (`batten statusline --install`)
```

Esa última línea es deliberada. La cuota se expone **solo** a la status line, así que sin ella
batten no puede verla — y lo dice en vez de imprimir `0%`.

**batten nunca inventa un número.** Un presupuesto que reporta cero calladito para lo que no pudo
medir es peor que ningún presupuesto, porque se le va a creer.

#### La métrica que nadie más reporta

Hay todo un ecosistema midiendo lo que cuestan los agentes — Langfuse, OpenTelemetry, ccusage,
CloudZero. **Todos miden dólares de API.** Con una suscripción esa es la unidad equivocada: el costo
marginal de un token es cero, y lo que de verdad se acaba es la ventana rodante.

Nadie con un plan Max quiere saber cuánto "habría costado" su tarde por API. Quiere saber si a las
cinco todavía le va a quedar cuota.

`quota_pct_per_run` es ese número, y `batten statusline` es el único sensor local que lo ve — la
cuota se expone a la status line y a ningún otro lado, y por eso batten puede leerla y un dashboard
no.

---

## Instalación

> **El camino del marketplace es el camino.** Una auditoría de la ruta de instalación —lo único que
> este proyecto había verificado *leyéndolo* en vez de ejecutándolo— encontró que `bootstrap.sh`
> descargaba a `${CLAUDE_PLUGIN_DATA}/bin` mientras cada hook y el servidor MCP invocan
> `${CLAUDE_PLUGIN_ROOT}/bin/batten`, así que una instalación de marketplace anunciaba éxito y
> después no gobernaba nada. Eso está arreglado, junto con otros cuatro blockers de instalación que
> la misma auditoría destapó, y la suite ahora EJECUTA los dos scripts de bootstrap contra un
> archive real en vez de leerlos.
>
> `v0.1.0-beta.1` está publicado, y la mitad de la descarga ya se ejercitó contra él: seis assets y
> un `checksums.txt`, seis 200 por `releases/latest/download`, cada hash verificado, y el
> `bootstrap.sh` real instalando desde ahí en un plugin root donde `batten version` contesta con el
> tag. Compilar desde fuente sigue siendo una ruta soportada, y es sobre la que corre este repo:
>
> ```console
> $ git clone https://github.com/ArthurZizumbo/batten && cd batten
> $ go build -o plugin/claude-code/bin/batten ./cmd/batten   # .exe en Windows
> ```

```
/plugin marketplace add ArthurZizumbo/batten
/plugin install batten
```

**La guía completa de instalación es [`docs/INSTALL.es.md`](docs/INSTALL.es.md)**
([en](docs/INSTALL.md)) — Windows sin Git Bash, compilarlo vos en vez de descargarlo (`go install`
incluido), dónde vive el estado y por qué, adoptar a mitad de sprint, y correr dos sesiones en
paralelo.

> **En Windows, esperá que tu antivirus se queje al menos una vez.** Defender clasifica binarios de
> Go recién compilados y sin firmar como `Trojan:Win32/*!ml` — un veredicto de machine learning, no
> una firma. Le pasó al binario de este proyecto: builds byte-idénticos dieron respuestas distintas
> y un re-escaneo explícito de esos mismos bytes volvió limpio, que es la forma de un falso
> positivo. batten todavía no está firmado con un certificado Authenticode, así que te puede pasar.
>
> Importa más que un cartel feo. Si el binario cae en cuarentena **después** de instalarse, cada
> hook queda apuntando a un archivo que ya no existe, mueren en silencio, y `batten doctor` no
> puede avisarte porque doctor *es* el binario que falta. El bootstrap ahora nota el patrón —una
> segunda restauración desde el caché dentro del mismo día— y lo dice en `SessionStart`. Si ves ese
> mensaje, mirá la cuarentena de tu antivirus.

**El binario está pensado para llegar solo.** Un hook `SessionStart` corre `bootstrap.sh`, que baja
el binario estático de tu plataforma desde el GitHub Release. Un build de desarrollo
(`scripts/build-plugin.sh`) lo deja en el `bin/` del propio plugin, y el bootstrap lo ve y sale. El
repo envía `bin/` **vacío**; los binarios commiteados inflan el repo y se quedan viejos.

Vale explicarlo, porque la alternativa es un bug real y común: un plugin cuyo `.mcp.json` invoca un
**nombre de comando pelado**, esperando que hayas instalado el binario aparte por brew o
`go install`. Cuando no lo hiciste, el servidor MCP falla *en silencio*, los hooks fallan *en
silencio*, y el gate que se suponía que te protegía simplemente no está. batten apunta al binario de
forma absoluta (`${CLAUDE_PLUGIN_ROOT}/bin/batten`) y, si la descarga falla, lo dice y no-opea en
vez de fingir que te cuida.

Los hooks son **forma exec** — el binario lee el JSON del hook por stdin. Sin `bash`, sin `jq`, sin
`curl`. **Windows es un target de primera**, no una idea tardía: la resolución en forma exec sin la
extensión `.exe` está verificada funcionando en Windows 11.

Después, en tu repo:

```console
$ batten init                              # entrevista el repo, escribe batten.yaml
$ batten init --from docs/workflow.md      # ...o migra un flujo que ya escribiste en prosa
$ batten doctor                            # valida el spec; reporta qué capacidades están vivas
```

`init` deriva lo que se puede derivar honestamente —tu patrón de unit de los nombres de rama, tus
dominios del layout, tus comandos `check` **verbatim** de tus archivos de build— y deja el resto
como TODOs explícitos en vez de adivinar. Se niega a pisar un `batten.yaml` que ya exista.

`--from` importa más de lo que parece: un spec solo es "general" si migrar a él es barato. Si
adoptar batten cuesta una tarde, nadie lo adopta.

Specs trabajados a tres profundidades, de 30 a 220 líneas, en [`examples/`](examples/).

## Los comandos

| comando | qué hace |
|---|---|
| `/batten-init` | entrevistar el repo (o un doc en prosa) y escribir `batten.yaml` |
| `/batten-plan` | decidir los dominios, las sub-tareas paralelas y sus **write-sets disjuntos** |
| `/batten-build` | el fan-out: un subagente por dominio y por sub-tarea, cada uno cercado a su write-set |
| `/batten-verify` | el gate: chequear el diff contra los criterios, emitir un veredicto con evidencia citada |
| `/batten-close` | procedencia, artefacto de resolución, y el commit que el gate tiene que permitir |
| `/batten-night` | desatendido: build → verify → fix → re-verify, deteniéndose antes del cierre |

Los *nombres* de las fases salen de tu `batten.yaml`. Los comandos leen el spec y corren la fase que
corresponda — no hardcodean un flujo.

El CLI que hay debajo es lo bastante chico como para usarlo directo, y
[`docs/QUICKSTART.es.md`](docs/QUICKSTART.es.md) recorre el camino de adopción completo así — de un
directorio vacío a un commit denegado y de vuelta — con salida capturada de una corrida real.

Leelo en ese orden — el gate es lo último que se prende, no lo primero:

| comando | qué hace |
|---|---|
| `batten demo` | el flujo entero sobre un repo que batten arma y tira. No toca nada tuyo |
| `batten report` | qué vio batten, y qué frenó. Para esto está el modo `report` |
| `batten init` | leer el repo y escribir `batten.yaml`, en modo `report` |
| `batten doctor` | una pasada, todo lo que sabe, con el arreglo al lado de cada problema |
| `batten phase <unit> <fase>` | abrir o avanzar una corrida; graba el SHA del ancla |
| `batten verdict --file v.json` | grabar el juicio del revisor, con evidencia citada |
| `batten check <unit>` | **correr** los checks declarados del gate y grabar lo que imprimieron |
| `batten close <unit>` | cerrar por el gate y liberar los claims de write-set |
| `batten pr <unit>` | un cuerpo de PR desde el registro: el DAG real como Mermaid, evidencia, costo |
| `batten recover <unit>` | re-anclar una corrida a la que se le movió la base (rebase, amend, pull) |
| `batten status` | el backlog contra el registro: cada work item, su estado, su cobertura de criterios |
| `batten scan-diff <unit>` | el diff real de git contra los write-sets declarados. Sin parseo de shell, sin falsos positivos |
| `batten worktree <unit>` | un árbol por work item; el merge de vuelta está gateado como un commit |
| `batten unattended <unit>` | nadie mira: cuatro reglas se vuelven denegaciones |
| `batten iterate <unit>` | contar una vuelta de fix→re-verify; se niega en el techo |
| `batten budget [<unit>]` | el gobernador: tokens, $ imputados, porción de la cuota rodante |
| `batten measure` | gasto por modelo, si las capacidades opcionales se pagaron solas, y cuánto sobre-declaran los write-sets |

Dos de esos merecen una frase cada uno, porque cierran huecos que los otros comandos no podían.

**`batten scan-diff`** es el chequeo que no lee shell. El guard de escrituras por Bash tiene un
límite que declara en voz alta: no puede ver una escritura hecha por un script de `python`, un
target de Makefile o un `go run`, porque ningún parser de comandos llega adentro. `scan-diff` le
pregunta a git qué cambió y a la base quién reclamó qué, y los contrasta — así un generador de
código queda tan visible como un `sed`. Se niega a concluir dos cosas que no puede saber: *quién*
tocó un archivo sin reclamar (desde un diff, el orquestador integrando y un agente cruzando la cerca
se ven idénticos), y que una corrida con cero claims esté limpia. Cero claims es un hueco de
planeación, y llamarlo limpio sería el tilde verde más vacío que existe.

Cada corrida además se **graba**, que es lo que convierte un contraste en una medición.
`batten measure` reporta la mediana de sobre-declaración sobre tus corridas escaneadas, con el
número de corridas sobre el que se calculó y —aparte, jamás sumado como cero— cuántas reclamaron
rutas y nunca se escanearon. S-Bus ([arXiv:2605.17076](https://arxiv.org/pdf/2605.17076)) midió
32–49 % de sobre-declaración en read-sets reconstruidos *automáticamente*; si los write-sets
declarados a mano hacen lo mismo es una pregunta sobre tu repo, y este es el único lugar donde se
contesta con tus datos.

**`batten status`** es la vista que `batten runs` no puede ser. Un work item que nadie arrancó no
tiene corrida que listar, y esos son justamente los que querés ver:

```console
$ batten status
backlog docs/backlog.md — 3 unit(s)

US-001  rate limit          ✓ closed ok        AC 2/2 covered
US-002  retry budget        ◐ running (verify)  AC 1/3 covered
US-003  audit log           · not started

not in the backlog: US-099 (running)
```

`/batten-night` es el que hay que leer antes de confiar. Nunca borra nada (si *quisiera*, te lo
cuenta en el reporte de la mañana), nunca pisa el gate, y se detiene antes del cierre. Los techos de
presupuesto son el fusible que a una corrida desatendida le falta — no hay ningún humano despierto
para notar que se quedó moliendo el mismo test rojo hasta que la ventana se fue.

## El run graph

```console
$ batten canvas US-034
```

Escribe un archivo [JSON Canvas 1.0](https://jsoncanvas.org/). Abrilo en Obsidian: las fases, el
fan-out, los reintentos, el veredicto bloqueado que se arregló y se re-verificó. Dibuja el camino
que el trabajo **realmente tomó**, no el que el plan esperaba.

Cero líneas de código de layout de grafos de nuestro lado. Obsidian ya lo renderiza.

Y el mismo grafo va al pull request, donde la gente que revisa el trabajo lo va a ver de verdad —
GitHub renderiza Mermaid nativamente en la descripción de un PR:

```console
$ batten pr US-034 --out /tmp/body.md
$ gh pr create --body-file /tmp/body.md
```

El cuerpo lleva el DAG que la corrida realmente tomó (incluyendo el reintento que un diagrama de
plan no puede mostrar), los dos veredictos con su evidencia citada, y cuánto costó. Si el uso nunca
se midió, la tabla dice **NOT MEASURED**, no `$0.00`; si nadie revisó el trabajo, la insignia lo dice
en vez de afirmar `batten-verified`. Un PR generado que adula la corrida es peor que ningún PR
generado.

---

## Lo que batten *no* hace

Una lista honesta, porque las partes concurridas de este espacio están concurridas de buenas
herramientas y deberías usarlas:

- **No guarda memoria episódica.** Lo que *decidiste*, y por qué, a lo largo de meses — eso es
  trabajo de [engram](https://github.com/Gentleman-Programming/engram), y lo hace bien. batten
  interopera; no compite.
- **No construye un grafo de código.** Lo que el código *es*, ahora mismo — eso es trabajo de
  [graphify](https://github.com/Graphify-Labs/graphify) (tree-sitter, determinístico, cero tokens de
  LLM). batten lo consulta si lo tenés y cae a grep si no.
- **No re-orquesta al agente.** Los Dynamic Workflows de Claude Code ya corren el fan-out, y lo
  corren bien. batten lo **gobierna** — los rieles, no el motor. Rehacer el orquestador sería un
  orquestador peor y un uso mucho peor del tiempo de todos.
- **No comprime tu contexto.** Si [headroom](https://github.com/headroomlabs-ai/headroom) ahorra
  tokens en *tu* fan-out, usalo — y como batten ya cuenta tokens por nodo, podés averiguar si de
  verdad lo hace en vez de creerle a un README.

Hay tres memorias en un agente de código. La **estructural** (qué *es* el código) y la **episódica**
(qué *decidimos*) están resueltas. La tercera —la **procedural**, *cómo trabajamos*— era la que nadie
tenía, y es lo único que batten reclama.

## Las capacidades opcionales degradan, a propósito

Todo lo que está bajo `capabilities:` es opcional, y cada una **degrada con gracia**:

```yaml
capabilities:
  graph:      { provider: graphify, query_before_read: true }
  memory:     { provider: engram }
  obsidian:   { vault: ~/vaults/acme, export: [runs, verdicts, canvas] }
  compression:{ provider: headroom, measure: true }
```

¿Sin proveedor de grafo? La fase de plan hace grep. ¿Sin vault? No se escribe canvas. ¿Sin status
line? El techo de cuota se reporta como no medible y los otros dos siguen mordiendo.

**Tené en cuenta que estas dependencias son pre-1.0** y se mueven rápido — graphify y headroom
publican cambios rompientes, y el propio hook `PreToolUse` de graphify ya se rompió contra un
release de Claude Code. Precisamente por eso son capacidades declaradas y no dependencias duras: el
núcleo de batten no las importa, no las llama en ningún camino crítico, y no falla cuando están
ausentes o rotas. Una dependencia opcional que falta debería costarte un fallback, nunca una
corrida.

## Estado

**Pre-1.0, y con dogfood.** batten está instalado en este repo y gobierna su propio desarrollo: el
último work item de acá se planificó, se abrió a dos subagentes con write-sets disjuntos, se
verificó, y se cerró por el propio gate de batten. Usarlo así es lo que encontró los últimos siete
bugs.

Los gates están verificados funcionando —un commit sin evidencia citada se deniega, un subagente
escribiendo el archivo de otro se deniega— igual que la parte que era un riesgo de diseño abierto:
la resolución de hooks en Windows, y si `agent_id` llega siquiera a `PreToolUse` (el write-set guard
cuelga de ese campo, y degrada a advisory *en voz alta* cuando falta).

**Y ya se corrió fuera de este repo.** Un field test multiagente puso a batten frente a agentes que
nunca lo habían visto, sobre una réplica de un proyecto real y sobre un repo construido desde un
directorio vacío: 90 comportamientos confirmados funcionando y 80 hallazgos, cada uno entregado
después a un segundo agente cuyo trabajo era refutarlo. 52 sobrevivieron esa pasada. Tres eran
blockers, y uno de esos era una regresión introducida cuatro commits antes, en la misma sesión, por
un cambio que yo había escrito, revisado y creído.

Todos los blockers están arreglados, cada uno con un test que falla contra el commit anterior. El
relato completo —qué se rompió, qué se refutó y por qué, y en qué acertó el método— está en
[docs/FIELD-TEST.es.md](docs/FIELD-TEST.es.md) ([en](docs/FIELD-TEST.md)).

Desde entonces la réplica del field test se **reconstruyó como script commiteado**
([`scripts/replica-ui.sh`](scripts/replica-ui.sh)) y los ocho fixes se re-corrieron contra ella — un
repo sin código, sin archivos de build y sin historia de git, que es la forma que ninguno de los
sandboxes sintéticos tenía. Nada de lo arreglado se deshizo. Lo que sí apareció fueron dos silencios
y un verde falso, incluido el que más importaba: **el primerísimo commit después de instalar batten
no daba ninguna salida**, que es indistinguible de una aprobación. Eso está arreglado, y la matriz
que lo encontró ahora corre como script commiteado:
[`scripts/matrix-replica.sh`](scripts/matrix-replica.sh), 41 aserciones.

### Dónde están los 52 hallazgos

Esos 52 hallazgos confirmados se trabajaron después. **45 están arreglados y verificados; 7 siguen
abiertos.** Están enumerados, con reproducciones, bajo *Known gaps* en
[CHANGELOG.es.md](CHANGELOG.es.md) — porque un proyecto cuyo argumento entero es *"nunca reportes un
número que no tenés"* no tiene derecho a resumir su propia lista de defectos como "casi todo
listo".

*(Regla de conteo, porque este número estuvo mal dos veces: un hallazgo está **abierto** si es
CONFIRMED en `verified.json` y no tiene fix a HEAD, contado una vez. Los 7 son 6 cosméticos más un
LÍMITE declarado — una escritura que cruza la cerca por un heredoc de `python` o un target de
Makefile, que ningún parser de shell alcanza y que `batten scan-diff` responde de forma estructural.
Dos impresiones anteriores decían 15 y después 14: la primera es anterior al fix del typo, y la
segunda contaba dos veces un hallazgo bajo dos números.)*

Los seis que **bloqueaban a un adoptante externo** están cerrados, cada uno con un test que falla
contra el commit anterior — incluidos los dos que eran instancias del patrón que batten existe para
eliminar: un typo en una clave top-level de `batten.yaml` se ignoraba en silencio mientras `doctor`
imprimía un verde `enforcement: enforce — gates block`; y `batten claim` solo chequeaba colisiones
dentro de su propia corrida, así que a dos corridas concurrentes en un mismo checkout se les decía a
las dos que poseían el mismo archivo, y después el guard denegaba a ambas.

Lo que **todavía no** pasó, dicho sin vueltas: **batten no fue adoptado por un proyecto al que no
pertenece, con gente que no lo escribió.** Esa es toda la razón por la que la versión dice beta, y
es el único hueco que ninguna cantidad de trabajo interno cierra. El binario ya se instala desde un
release publicado —verificado punta a punta— pero "se instala" y "otra persona lo usa" son
afirmaciones distintas y solo la primera está probada.

Los bootstraps verifican el **sha256** del release antes de instalar nada y se niegan a instalar lo
que no coincide, pero no verifican **firma** — una cuenta de release comprometida reemplaza el asset
y su línea de checksum juntos, así que esa mitad de la cadena de suministro sigue abierta. En
Windows el binario tampoco está firmado para Authenticode, que es la razón por la que un antivirus
puede marcarlo (ver *Instalación*).

Y no hay GIF en este README — los scripts `.tape` que generan uno están escritos y verificados
([`docs/tape/`](docs/tape/)), pero la máquina donde se construyó esto no tiene `vhs` instalado, y
`batten demo` es la versión viva de todas formas.

El formato de transcript que batten lee para contabilizar tokens **no es una API pública** y puede
cambiar sin aviso; si el parseo se rompe, batten reporta el conteo como no disponible en vez de
adivinar.

El inventario completo —qué está probado, qué está apenas construido, qué falta, y las decisiones de
nombre todavía abiertas— está en [ROADMAP.es.md](ROADMAP.es.md) ([en](ROADMAP.md)). Lo que aterrizó
hasta ahora está en [CHANGELOG.es.md](CHANGELOG.es.md) ([en](CHANGELOG.md)).

MIT.
