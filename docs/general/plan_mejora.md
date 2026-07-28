# Plan de mejora

> Estado base: commit `24d7cd2`, versión `0.1.0`, suite verde, sin release. Escrito 2026-07-28
> después del field test, de sus 8 correcciones, y de un estudio comparativo de mercado externo.
>
> **Regla de este documento:** cada afirmación dice de dónde sale — una medición propia, un
> hallazgo verificado del field test, o una fuente citada. Donde el estudio externo se equivoca,
> lo digo. Donde acierta, lo digo también.
>
> Contexto del plugin: [`plugin_al_momento.md`](plugin_al_momento.md) ·
> Field test: [`../FIELD-TEST.md`](../FIELD-TEST.md) ·
> Recorrido de adopción: [`../QUICKSTART.md`](../QUICKSTART.md)

---

## 0. Cómo leer este plan

Tres fuentes de evidencia alimentan lo que sigue, y conviene no confundirlas:

| fuente | qué aporta | confianza |
|---|---|---|
| **field test** (63 hallazgos verificados adversarialmente) | defectos reproducidos en el binario real | alta — cada uno con repro y control positivo |
| **mediciones propias** (esta sesión) | tokens de payloads MCP reales, consumidores de campos del spec | alta — reproducibles |
| **estudio comparativo externo** | posicionamiento de mercado, comparación con otros plugins | **media — dos afirmaciones suyas resultaron falsas al verificarlas** |
| **literatura Jun–Jul 2026** (10 papers) | valida o refuta decisiones de diseño | alta para lo empírico, con las salvedades de cada paper |

---

## 1. Auditoría del estudio externo

El estudio califica a batten **8.5/10 — "Innovación de Integración de Alta Viabilidad"**, con el
juicio de que su valor no está en conceptos inéditos sino en la síntesis coherente. **Estoy de
acuerdo con ese juicio.** Pero antes de planear sobre él, hay que separar lo verificado de lo que
no lo está.

### 1.1 — Lo que el estudio acierta (confirmado contra el código)

| afirmación del estudio | verificación |
|---|---|
| Evasión del write-set guard por `Bash` (`sed -i`, `cat >>`, heredocs) | **CONFIRMADO** — hallazgo #6 del field test, reproducido. `preToolUse` rutea `Bash` solo a `verdictGate`; el comando nunca se contrasta contra las rutas reclamadas |
| Un claim de directorio no ofrece control recursivo | **CONFIRMADO** — hallazgo #7. Se acepta, se reporta como protector, y no cerca nada |
| Fail-open silencioso ante panic / DB corrupta / JSON malformado | **CONFIRMADO** — [`main.go:168`](../../cmd/batten/main.go#L168) recupera el panic y pone `retErr = nil`; `loadForHook` y `Dispatch` también retornan `nil` ante error. Exit 0 sin salida, indistinguible de ALLOW |
| Incoherencia entre spec declarado e implementación | **CONFIRMADO** — 5 campos con cero consumidores, medido grepeando. Ver §6 |
| Obsidian es exportación pasiva unidireccional, solo en `Stop` | **CONFIRMADO** — `export.Run` dispara desde `Stop`, `canvas` y post-veredicto. No hay canal de lectura |

### 1.2 — Lo que el estudio se equivoca

**(a) CRLF en `bootstrap.sh` — ya resuelto, hace tiempo.**

El estudio lo lista como vulnerabilidad abierta. Es falso: [`.gitattributes`](../../.gitattributes)
fija `*.sh text eol=lf` explícitamente, con un comentario que explica exactamente el modo de
falla que el estudio describe. Y CI verifica dos contratos relacionados: que la copia del
bootstrap dentro del plugin esté sincronizada con su fuente, y que el nombre de artefacto que
pide `bootstrap.sh` coincida con el que produce GoReleaser.

Probablemente el estudio analizó una instantánea previa al commit `8e038b5`.

**(b) La escala del ecosistema — no verificable como se afirma.**

El estudio dice "más de 56.000 plugins y 556.000 componentes individuales, incluyendo 11.500
ganchos y más de 8.400 servidores MCP", y usa esa cifra para sostener que las funciones de batten
"coinciden con herramientas consolidadas".

Lo que sí es verificable: el directorio oficial de Anthropic tiene **~55 plugins vetados**, el
marketplace comunitario **~70 que pasaron filtro automático de seguridad**, y para mayo de 2026 el
marketplace oficial lista **cientos** de plugins con **2.000+ skills** en marketplaces
comunitarios ([Claude Camp](https://claudecamp.ai/blog/claude-code-plugins-official-directory)).

La cifra de 56.000 parece ser de un agregador que cuenta repositorios de GitHub, no de un
directorio curado. **La diferencia importa para el argumento**: un ecosistema de decenas de
plugins vetados es uno donde batten compite con pocos pares serios; uno de 56.000 es donde se
pierde. No hay que planear como si fuera lo segundo sin evidencia.

**(c) La fórmula de eficiencia de concurrencia.**

El estudio propone `E = (T_util − T_bloqueo) / (T_util + T_reparación)`. Es una intuición
razonable pero no es de ninguna fuente citada y no está dimensionalmente motivada: mezcla un
numerador que puede ser negativo con un denominador que siempre crece. Trátese como heurística
ilustrativa, no como métrica. **La literatura real sobre esto está en §3**, y es más útil.

---

## 2. Resumen de decisiones

| tema | decisión | evidencia |
|---|---|---|
| **TOON** en vez de JSON | **descartado** | medición propia: +5.7 % tokens. Y la literatura añade un riesgo peor: pérdida de exactitud |
| **Duplicación de payload MCP** | **rediseñar — prioridad máxima** | el patrón correcto está documentado y batten lo usa al revés |
| **Fail-open en hooks** | **arreglar, pero NO como propone el estudio** | fail-closed brickearía sesiones; la respuesta correcta es fail-open **ruidoso** |
| **Bypass por Bash** | **arreglar — prioridad alta** | hallazgo #6 + el estudio + la literatura de control planes |
| **TUI: seguimiento de cumplimiento** | **sí, hacerlo** | el dato de criterios no existe |
| **Validar contra proyecto_ui** | **sí, va primero** | los 8 fixes nunca se revalidaron contra la forma difícil |
| **Cadena graphify → engram** | **sí — no estaba considerada** | el subagente del fan-out no consulta nada |
| **Migrar a concurrencia optimista (OCC)** | **no todavía — pero adoptar su principio** | CoAgent valida la dirección; el costo de implementarla no está justificado a 0.1.0 |
| **Obsidian bidireccional en vivo** | **no en este ciclo** | alto costo, valor no demostrado; ver §7.3 |

---

## 3. Lo que dice la literatura de junio–julio de 2026

Diez trabajos, elegidos porque tocan directamente una decisión de batten. Lo relevante no es que
existan sino qué obligan a cambiar.

### 3.1 — La tesis de batten queda validada empíricamente

**[Reason Less, Verify More: Deterministic Gates Recover a Silent Policy-Violation Failure Mode
in Tool-Using LLM Agents](https://arxiv.org/html/2607.07405v1)** — arXiv:2607.07405, 8 de julio
de 2026 (Reddy, Challaram, Basu; KDD Workshop on Evaluation and Trustworthiness of Agentic AI).

Es el paper más directamente relevante que existe para este proyecto. Sus números:

- En las tareas de aerolínea de τ²-bench, **el 78 % de las fallas observadas son fallas
  silenciosas de estado incorrecto, sin ningún error de herramienta.**
- Compuertas deterministas de pre-ejecución llevaron el éxito de **29,6 % a 42,0 % (+12,4 pp)**,
  replicado en un conjunto disjunto de 15 semillas a **+12,3 pp (P = 0,0008)**.
- En las tareas donde la compuerta efectivamente disparó (26 de 50), la mejora fue **+19,2 pp**.
- Bajo la métrica de fiabilidad pass₅: **26,0 % con compuerta contra 8,0 % sin ella** — más del
  triple.
- Y la frase que describe exactamente el fallo que batten existe para matar: el agente *"puede
  reportar con confianza que la tarea está completa"* mientras el estado del sistema está
  corrupto, porque la herramienta permisiva no da ninguna señal correctiva.

**Qué significa para batten:** la premisa fundacional —que un agente que se autoevalúa aprueba
trabajo roto y que una compuerta determinista lo recupera— no es una intuición de diseño, está
medida. También refuerza el **doble veredicto**: la mitad `source: batten` es exactamente la
"señal correctiva" que el paper dice que falta.

### 3.2 — El fail-open es la vulnerabilidad que la literatura señala más fuerte

**[A Deterministic Control Plane for LLM Coding Agents](https://arxiv.org/html/2606.26924v1)** —
arXiv:2606.26924, junio de 2026.

Despliega ganchos `PreToolUse` de Claude Code que filtran *tanto* pre-escritura (contenido de
archivo) *como* pre-ejecución (**cada llamada Bash**), y enfatiza **diseño fail-closed** en todo.
Añade una segunda capa: un `scan-diff` post-delegación que revisa **solo las líneas añadidas** del
diff de git, y una máquina de estados que se niega a completar la fase hasta que el escaneo esté
limpio.

**[ActPlane: Programmable OS-Level Policy Enforcement for Agent
Harnesses](https://arxiv.org/html/2606.25189v2)** — arXiv:2606.25189, junio de 2026, y
**[Reframing LLM Agent Security as an Agent–Human Interaction
Problem](https://arxiv.org/pdf/2605.24309)** — arXiv:2605.24309.

El segundo ordena los mecanismos por fiabilidad de despacho: los ganchos de framework (incluidos
los de Claude Code) están **un escalón arriba en facilidad de despliegue y un escalón abajo en
determinismo** frente a la aplicación a nivel de SO (Seatbelt en macOS, Bubblewrap en Linux).

**Qué significa para batten:** dos cosas, y la segunda es la que el estudio externo no ve.

1. El bypass por Bash no es un detalle: los dos control planes de la literatura filtran **cada
   llamada Bash**, no solo las herramientas de escritura declaradas. batten es el que está
   incompleto aquí.
2. **Pero "fail-closed" no se puede adoptar tal cual.** El estudio externo recomienda que
   *cualquier* retorno nulo por fallo de DB, ausencia de configuración o panic fuerce un DENY.
   Eso rompe el principio #2 de batten —*degradar, nunca romper*— de una forma concreta: un
   `busy_timeout` de SQLite bajo contención de dos sesiones denegaría cada llamada a herramienta y
   dejaría la sesión inutilizable. Un gate que brickea el día que la DB está ocupada se
   desinstala esa misma tarde.

   **La respuesta correcta es una tercera:** *fail-open ruidoso*. El commit pasa, y batten emite un
   `systemMessage` explícito diciendo que **la compuerta no se ejecutó**. El silencio nunca debe
   ser indistinguible de la aprobación — que es literalmente el principio #3 del proyecto, hoy
   aplicado a la atribución pero no a los fallos internos. Ver §4.2.

### 3.3 — La concurrencia: CoAgent valida la dirección, no la migración inmediata

**[CoAgent: Concurrency Control for Multi-Agent
Systems](https://arxiv.org/abs/2606.15376)** — arXiv:2606.15376, 13 de junio de 2026 (Shanghai
Jiao Tong University).

Su diagnóstico aplica a batten con precisión incómoda: las transacciones de agentes *"abarcan
minutos de inferencia, con conjuntos de lectura amplios y opacos"*; los bloqueos bloquean
intervalos largos de inferencia, y el OCC clásico de abortar-y-reintentar **descarta minutos de
trabajo en cada conflicto**.

Su protocolo **MTPO** (Monotonic Trajectory Pre-Order) fija un orden de serialización al lanzar,
sirve a cada lectura el valor filtrado por ese orden, aplica escrituras especulativamente in
situ, y envía una **notificación unidireccional** pidiendo al lector afectado que re-juzgue y
parche su plan; el framework deshace y reordena mecánicamente mediante inversas estilo saga.

La frase clave, y la razón de que esto importe más de lo que parece:

> el control se vuelve **advisory**, donde el runtime informa y el agente repara.

**Qué significa para batten:** eso es exactamente la filosofía del `advise()` que batten ya tiene
para la atribución — informar y dejar que el agente actúe — aplicada a la concurrencia. batten ya
tiene el vocabulario conceptual correcto; lo que no tiene es aplicarlo al write-set.

**Pero la migración a OCC no está justificada hoy**, y conviene decir por qué en vez de asumirlo:

- MTPO requiere inversas saga por operación, versionado de estado y un orden preacordado. Es un
  proyecto grande.
- batten no tiene aún **ninguna medición** de `T_bloqueo` real. No sabemos si los subagentes se
  bloquean entre sí lo suficiente como para que importe: un fan-out con write-sets bien
  planeados es *disjunto por construcción*, y el bloqueo solo aparece cuando el plan estuvo mal.
- El paso barato y previo es **medirlo**: registrar cada denegación del write-set guard con su
  duración, y ver si el bloqueo es un problema real antes de rediseñar por él.

**[S-Bus: Automatic Read-Set Reconstruction for Multi-Agent LLM State
Coordination](https://arxiv.org/pdf/2605.17076)** — arXiv:2605.17076, mayo de 2026, aporta el
dato más accionable de todos para batten:

> **Los agentes sobre-declararon su uso de recursos entre 32 % y 49 %**, según el método de
> evaluación.

Y por eso S-Bus reconstruye el read-set observando el tráfico real en vez de confiar en la
declaración del agente. **batten depende enteramente de que el subagente declare su write-set
honestamente vía `batten claim`.** Si los agentes sobre-declaran entre un tercio y la mitad, el
bloqueo pesimista de batten está cercando muchos más archivos de los que realmente toca — lo que
convierte el punto de CoAgent sobre `T_bloqueo` en algo mucho más probable de lo que parecía.

**Acción concreta y barata que sale de aquí:** comparar el write-set *declarado* con el
*efectivamente escrito* (batten ya ve ambos: la tabla `writesets` y los eventos `PostToolUse`) y
reportar la sobre-declaración. Es medición, no rediseño, y decide si §7.1 vale la pena.

### 3.4 — La memoria procedimental como categoría queda respaldada

**[Managing Procedural Memory in LLM Agents: Control, Adaptation, and
Evaluation](https://arxiv.org/abs/2606.23127)** — arXiv:2606.23127, 22 de junio de 2026.

Introduce el benchmark **AFTER**: 382 tareas empresariales realistas, seis roles profesionales,
22 skills procedimentales. Resultados: una sola ronda de refinamiento mejora el desempeño
agregado **3,7–6,7 puntos**, y las skills evolucionadas desde trazas de ejecución multi-modelo
alcanzan **73,1 % de exactitud cross-model**.

**[Neural Procedural Memory: Empowering LLM Agents with Implicit Activation
Steering](https://arxiv.org/abs/2606.29824)** — arXiv:2606.29824, junio de 2026. Representa la
memoria procedimental como vectores de dirección en el espacio de activaciones, sin entrenamiento.

**Qué significa para batten:** la tripartición que batten usa —estructural (graphify) / episódica
(engram) / procedimental (batten)— es la que la literatura de 2026 también usa. La memoria
procedimental como *dato de primera clase* es una línea de investigación activa, no una
invención local. Y el resultado de AFTER —que refinar la memoria procedimental mejora el
desempeño de forma medible— sugiere que el paso siguiente natural de batten no es solo *imponer*
el proceso sino **aprender de las corridas cuál funciona**, que es algo que el schema hoy no
contempla en absoluto.

**[Autoformalization of Agent Instructions into
Policy-as-Code](https://arxiv.org/pdf/2606.26649)** — arXiv:2606.26649, junio de 2026. Gobierna
al agente vía un motor de políticas determinista externo que evalúa acciones contra reglas
formales. Es, literalmente, la generalización de lo que hace `batten init --from` **cuando llegue
a funcionar** (hoy solo imprime su argumento — hallazgo #0 del field test).

### 3.5 — TOON: la literatura confirma el descarte, y por una razón peor

**[Notation Matters: A Benchmark Study of Token-Optimized Formats in Agentic AI
Systems](https://arxiv.org/html/2605.29676v1)** — arXiv:2605.29676, 28 de mayo de 2026. Compara
JSON, TOON y TRON en cuatro benchmarks, incluyendo interacciones MCP.

Sus hallazgos coinciden con mi medición y añaden algo que yo no medí:

| hallazgo | número |
|---|---|
| TOON reduce tokens | hasta **−18 %** |
| TOON **pierde exactitud** | hasta **−9 pp** |
| TRON gana en datos estructuralmente repetitivos | hasta −27 % |
| TRON **pierde** en esquemas dispersos | hasta **+21 % tokens** |
| la sensibilidad al formato **depende del modelo** | Mistral-Small-24B: **+13 pp** en un benchmark, **−36 pp** en otro |
| penalización multi-turno | fallos de parseo dispararon iteraciones extra: Qwen3-32B terminó en **+8–11 % tokens totales** pese a comprimir por llamada |

**La conclusión del paper coincide exactamente con mi medición**: el formato tabular solo paga
cuando el lote tiene patrones estructurales repetidos, y **cuesta más** cuando cada payload
referencia pocos esquemas únicos — que es la forma de `batten_spec` y `batten_run_graph`.

**Descarte confirmado, y reforzado.** Mi razón era el costo (+5,7 %); la literatura añade una
peor: **riesgo de exactitud dependiente del modelo**, de hasta −9 pp para TOON y con varianza
brutal entre benchmarks. Un plugin cuyo propósito es *no mentir sobre lo que sabe* no puede
adoptar un formato de serialización que degrada la comprensión del modelo de forma impredecible.

---

## 4. Prioridad máxima — antes de construir nada encima

### 4.1 — El payload MCP: batten usa el patrón exactamente al revés

**Mi medición.** Capturé los payloads reales del servidor MCP contra la base del demo:

| herramienta | `content[].text` | `structuredContent` | idénticos |
|---|---:|---:|:--:|
| `batten_runs` | 932 chars | 932 chars | sí |
| `batten_run_graph` | 1176 | 1176 | sí |
| `batten_verdict_status` | 885 | 885 | sí |
| `batten_spec` | 1466 | 1466 | sí |

**Lo que la investigación añade, y que cambia el diagnóstico.** No es simplemente "se cobra dos
veces". Los dos campos tienen **propósitos distintos**: `structuredContent` está pensado para el
cliente —renderiza un widget interactivo— y **nunca entra al contexto del modelo, así que cuesta
cero tokens**. El patrón recomendado es dividir la respuesta: `content` con un **resumen textual
compacto** (solo para el modelo) y `structuredContent` con los datos ricos (solo para el cliente)
([futuresearch.ai](https://futuresearch.ai/blog/mcp-results-widget/); ver también el
[release candidate 2026-07-28 de la especificación
MCP](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/), que amplía
`structuredContent` a cualquier valor JSON).

**batten hace lo contrario:** mete el JSON completo en `content` —donde el modelo lo paga, y en
la forma más cara posible— y el mismo JSON completo en `structuredContent`, donde no hay widget
que lo consuma.

**Arreglo propuesto:** que `content[].text` sea prosa compacta y `structuredContent` conserve el
JSON íntegro. Concretamente, `batten_verdict_status` pasaría de ~885 caracteres de JSON a algo así:

```
US-034 · gate qa · BLOQUEADO
Falta el pase batten-verificado: los checks no se corrieron.
Corré: batten check US-034
```

Eso es la información que el agente necesita para actuar, en ~25 tokens en vez de ~260. **Y de
paso resuelve la pregunta de TOON sin adoptar TOON**: el ahorro viene de mandar menos, no de
codificar distinto.

**Costo:** ~1 día para las 6 herramientas. **Ahorro estimado: 70–90 % del contexto MCP**, contra
el −5,7 % que TOON *habría empeorado*. **Prioridad: la más alta del documento.**

> **Validación pendiente antes de escribir código:** confirmar empíricamente que Claude Code no
> mete `structuredContent` al contexto. Un turno con una llamada MCP y `/context` antes y
> después lo resuelve en una hora. Si resulta que sí lo mete, el arreglo sigue siendo correcto
> pero el ahorro es aún mayor.

### 4.2 — Fail-open ruidoso (NO fail-closed)

**El defecto, confirmado.** [`cmd/batten/main.go:163-197`](../../cmd/batten/main.go#L163):

```go
defer func() {
    if r := recover(); r != nil { retErr = nil }   // panic → silencio
}()
sp, st, err := loadForHook(raw)
if err != nil { return nil }                        // sin spec / DB rota → silencio
out, err := h.Dispatch(event, raw)
if err != nil || out == nil { return nil }          // error de dispatch → silencio
```

Seis causas distintas producen exit 0 sin salida, y Claude Code las lee todas igual: ALLOW.

**Por qué NO fail-closed, pese a lo que recomiendan el estudio y la literatura.** El principio #2
de batten es *degradar, nunca romper*, y tiene una razón operativa concreta: SQLite bajo
contención de dos sesiones devuelve `SQLITE_BUSY`. Con fail-closed, eso deniega **cada llamada a
herramienta** hasta que se libere. Una herramienta de gobierno que inutiliza la sesión el día que
la base está ocupada no sobrevive a la primera semana. El paper de control plane puede permitirse
fail-closed porque su plano de política es un proceso separado con su propia disponibilidad; el
de batten corre dentro del ciclo de vida de cada llamada a herramienta.

**La tercera opción, que es la correcta:** el commit pasa, **y batten dice que no evaluó nada.**

```
batten (advertencia — no bloqueando): la compuerta NO se ejecutó para este commit.
  causa: no se pudo abrir el store (database is locked)
  este commit NO fue verificado. Reintentá, o usá `batten doctor` para diagnosticar.
```

Eso preserva el principio #2 y satisface el #3 —*fallar abierto solo en voz alta*— que hoy se
aplica a la atribución pero **no a los fallos internos de batten**. Es la misma corrección que ya
hicimos para los commits no atribuibles (commit `f6aeba2`), extendida al camino de excepción.

**Detalle de implementación:** distinguir "acá no hay batten.yaml" (silencio correcto: no
gobernamos donde no nos invitaron) de "hay batten.yaml y algo se rompió" (aviso obligatorio). Hoy
ambos caen en el mismo `return nil`.

**Costo:** ~medio día. **Prioridad: máxima**, junto con 4.1.

### 4.3 — Validar los 8 fixes contra la réplica de proyecto_ui

**No se hizo, y es el hueco real.** Los 63 hallazgos se verificaron y los 8 fixes se validaron
sobre **sandboxes sintéticos** y sobre `taskly`, el demo desde cero. La réplica de proyecto_ui se
usó en la corrida original —sesión anterior— y ese sandbox ya no existe.

| | `taskly` (demo nuevo) | réplica de proyecto_ui |
|---|---|---|
| repo git | sí | **no lo es** — todo el modelo cuelga de git |
| código | sí, Go con tests | **ninguno**: 4 dominios con solo su `AGENTS.md` |
| dominios | 2 | 4 |
| backlog | 5 items | 40+ `US-0NN` |
| skills / agentes | 0 | 47 skills, 9 agentes custom |
| build files | `Makefile` | ninguno → `gates.checks` **vacío** |

Las dos últimas filas son las que muerden: **un repo sin build files cae en la rama del gate sin
checks**, que es exactamente donde vivía la regresión de `on_exceed: block`.

**Matriz mínima** (sandbox, `BATTEN_DB` exportado siempre, nunca el original):

1. `init` sobre 4 dominios sin código y 40+ items → ¿unit `US-\d{3}`, dominios completos,
   `gates.checks` vacío reportado como vacío?
2. Gate **sin checks** + presupuesto excedido → debe DENEGAR (la regresión de `24d8e4c`)
3. Dos units abiertos en la misma fase → los dos canvas intactos
4. `git -c user.name=... commit` → debe entrar al gate
5. Primer commit tras adoptar → debe **avisar**, no callar
6. Commit cuyo mensaje nombra otro unit → debe denegar
7. `ingest` con transcript previo al run → debe reportar lo descartado
8. **Repo sin `git init`** → ¿degrada con aviso o revienta? *No hay ningún test que cubra esto, y
   es el estado literal de proyecto_ui*

**Costo:** ~medio día. **Prioridad: máxima.** Es lo único que puede *invalidar* trabajo ya hecho.

### 4.4 — Deuda propia: el tercer sitio del un-solo-veredicto

`internal/export/export.go:58` usa `LatestVerdict`. Arreglé `batten show` (`92ae1cb`) y la TUI
(`24d7cd2`); el que escribe la nota de Obsidian y el canvas quedó leyendo solo el último — así
que la nota del vault tapa la evidencia del revisor con la salida de checks.

**Costo:** ~30 min con test. **Hacerlo primero por higiene**, es una inconsistencia conocida.

---

## 5. Prioridad alta

### 5.1 — Cerrar el bypass por Bash del write-set guard

**Evidencia convergente:** hallazgo #6 del field test (reproducido: `Edit` denegado, la misma
escritura por `Bash` pasa en silencio y el archivo se sobrescribe de verdad), el estudio externo,
y los dos control planes de la literatura que filtran **cada** llamada Bash.

**Lo que lo hace peor que un fail-open deliberado:** en ese punto batten **sí** atribuye —nombra
al dueño una llamada antes— y aun así no dice nada. No es "no puedo determinar culpa", es "no
miré".

**Arreglo por etapas, de menor a mayor riesgo de falsos positivos:**

1. **Detección de redirecciones y utilidades comunes** (`>`, `>>`, `tee`, `sed -i`, `mv`, `cp`,
   `patch`, `dd of=`). Cubre la mayoría de los casos reales con parseo simple.
2. **Resolver rutas relativas** contra el `cwd` del payload y contrastarlas con `writesets`.
3. **Advisory primero, deny después.** Empezar avisando durante un ciclo, medir falsos positivos
   sobre corridas reales, y solo entonces endurecer. El guard de `Edit` puede permitirse DENY
   porque la ruta es un campo estructurado; una línea de shell no lo es.
4. **`scan-diff` post-hoc como red de seguridad**, siguiendo a
   [arXiv:2606.26924](https://arxiv.org/html/2606.26924v1): tras el fan-out, contrastar el diff
   de git contra los write-sets declarados y reportar cada archivo escrito por quien no lo
   reclamó. Esto **no requiere parsear shell** y atrapa todo, incluidos los scripts de terceros
   que el punto 1 nunca verá.

> El punto 4 es la mejor relación valor/riesgo de los cuatro y probablemente debería ir primero:
> es determinista, no tiene falsos positivos, y produce exactamente el dato que §7.1 necesita
> (cuánto sobre-declaran los agentes, el 32–49 % de S-Bus).

**Costo:** 1 ~1 día · 2 ~medio día · 3 ~un ciclo de uso · 4 ~1 día. **Prioridad: alta.**

### 5.2 — Claim de directorio y colisión entre runs

- **#7** — un claim de directorio se acepta, se reporta como protector y no cerca nada.
  Arreglo: o rechazarlo con la forma de lista de archivos, o soportar `dir/**` de verdad en
  `writeSetGuard`. Cualquiera de las dos, pero no la actual.
- **#4** — `claim` no mira otros runs abiertos: dos sesiones creen poseer el mismo archivo, y
  después el guard las deniega a **ambas**. `store.WriteSetOwnerAcrossOpenRuns` ya existe; falta
  llamarla en el momento del claim, para que el error del plan falle temprano en vez de
  detonar como denegación mutua.

**Costo:** ~1 día los dos. **Prioridad: alta.**

### 5.3 — La cadena graphify → engram para el subagente

**No estaba considerada, y el hueco es real.**

| dónde | qué hace |
|---|---|
| `/batten-plan` | usa `graphify god-nodes --json` y `graphify affected "X"`, y `mem_search` antes de planear |
| `/batten-verify` | `mem_search "<el bug>"` antes de juzgar |
| `/batten-close` | escribe la decisión a engram |
| **`/batten-build`** | **nada.** Cero menciones de graphify o engram |

**El subagente del fan-out —el que efectivamente escribe código— no consulta nada.** Arranca
directo a leer archivos, que es el paso más caro en tokens de los tres.

Y los dos campos del spec que pedirían esa cadena **tienen cero consumidores**:
`capabilities.graph.query_before_read` y `phases[].graph_query`. Se escriben en el `batten.yaml`
que genera `init`, aparecen en README y DESIGN, y no gobiernan nada.

**El orden correcto corresponde a las tres memorias:**

1. **graphify** — *qué es* el código: quién llama a esto, qué se rompe si lo cambio. Barato,
   exacto, sin API key en modo `--code-only`.
2. **engram** — *qué decidimos*: si ya se resolvió, por qué se hizo así, qué falló la vez pasada.
3. **leer archivos / grep** — el fallback, y el más caro de los tres.

**Propuesta:**

- **Paso 1 (~2 h, es prompt):** que `/batten-build` inyecte en el prompt de cada subagente, cuando
  `query_before_read: true`, la instrucción de consultar el grafo, luego la memoria, y solo
  entonces leer — **y de declarar en su retorno si ninguno estaba disponible**, en vez de simular
  que consultó. Eso último es el principio #3 aplicado a la orientación.
- **Paso 2 (~2 h):** que `doctor` cruce la declaración con la realidad — `query_before_read: true`
  sin graphify en PATH debe avisar. Hoy no cruza esos dos datos.
- **Paso 3 (~medio día):** medirlo. batten ya marca cada run con si el grafo estaba fresco
  (`SetCodeGraph`) y ya cuenta tokens por nodo; `measure` puede comparar el costo de orientación
  con y sin grafo. Mismo trato que headroom: *admitido, pero medido*.

**Prioridad: alta** para el paso 1 — mejor relación esfuerzo/impacto del documento después de
§4.1, y cierra una promesa que el spec ya hace.

---

## 6. Declarado y no leído — la decisión de fondo

Cinco campos del spec tienen **cero consumidores** en todo el código Go. Medido grepeando, no
inferido:

| campo | qué promete | consumidores |
|---|---|---|
| `phases[].diff_from: anchor` | operar solo sobre el diff del unit | **0** (hallazgo #24) |
| `phases[].graph_query` | consultar el grafo en vez de grepear | **0** |
| `capabilities.graph.query_before_read` | preguntar al grafo antes de leer | **0** |
| `models.tiers` / `models.phases` | *"batten routes subagents and verifies it from the ledger"* | **0** — no rutea nada |
| `provenance.format` | metadatos de procedencia para auditoría | **0** |
| `edges.rel = retry_of` | reintentos visibles en el grafo | 4 consumidores, **0 productores** (#41) |

Un campo que el usuario escribe creyendo que gobierna, y que no gobierna, es **peor que su
ausencia**. Categorización útil para clasificar cualquier campo:

- **(a) impuesto por el binario** = mecanismo real
- **(b) pasado al agente vía MCP `batten_spec`** = una *declaración* que el modelo puede honrar
- **(c) leído por nadie** = ficción

**Recomendación campo por campo:**

| campo | decisión | por qué |
|---|---|---|
| `query_before_read`, `graph_query` | **implementar** | §5.3; es prompt, no motor |
| `retry_of` | **implementar** | barato, y hace legible un fan-out con reintentos — lo que CoAgent llama reparación necesita exactamente esta arista |
| `diff_from: anchor` | **implementar** | el ancla ya se graba; falta que `check` y `verify` la usen para acotar el diff |
| `models.tiers` / `models.phases` | **sacar del spec generado** | batten no puede cambiar el modelo del ciclo principal; sí podría rutear subagentes, pero eso es un proyecto. Documentarlo como futuro, no generarlo |
| `provenance.format` | **sacar del spec generado** | igual |

---

## 7. Lo que se evaluó y NO se hace en este ciclo

Decisiones negativas, con su razón — para que otra sesión no las reabra sin evidencia nueva.

### 7.1 — Migrar a concurrencia optimista (OCC / MTPO)

El estudio externo lo recomienda citando CoAgent. **La dirección es correcta; el momento no.**

Razones para esperar:
- MTPO requiere inversas saga, versionado y orden preacordado. Es un rediseño, no una mejora.
- **No hay ninguna medición de `T_bloqueo` en batten.** No sabemos si los subagentes se bloquean
  lo suficiente como para que importe. Un fan-out con write-sets bien planeados es disjunto por
  construcción; el bloqueo solo aparece cuando el plan estuvo mal.
- El dato de S-Bus (**32–49 % de sobre-declaración**) sugiere que sí podría importar — pero eso
  es una hipótesis a medir, no a asumir.

**Paso previo, barato:** instrumentar. Registrar cada denegación del guard con duración, y
comparar write-set *declarado* contra *efectivamente escrito* (§5.1 punto 4 produce ese dato). Si
la sobre-declaración de batten se parece al 32–49 % de S-Bus y el bloqueo es medible, entonces
§7.1 se reabre con evidencia.

**Lo que sí se puede adoptar ya, sin migrar nada:** el principio de CoAgent de que *el control
sea advisory — el runtime informa, el agente repara*. batten ya tiene `advise()`; extenderlo a
las colisiones de write-set de baja severidad, en vez de denegar siempre, es barato y está
alineado con la literatura.

### 7.2 — TOON

**Descartado.** Mi medición: +5,7 % de tokens en el conjunto real. La literatura
([arXiv:2605.29676](https://arxiv.org/html/2605.29676v1)) añade la razón que pesa más: **hasta
−9 pp de exactitud**, con sensibilidad dependiente del modelo tan grande que el mismo modelo ganó
13 pp en un benchmark y perdió 36 en otro.

Reevaluar solo si aparece un payload genuinamente tabular y grande (un `batten_runs` histórico de
50+ filas, o un export del ledger de usage) **y** midiendo exactitud, no solo tokens.

### 7.3 — Obsidian bidireccional en vivo

El estudio propone evolucionar a un servidor MCP interactivo estilo `graph-context-for-claude-code`.
Es una idea razonable y **no está descartada en el largo plazo**, pero no en este ciclo:

- El valor está sin demostrar: nadie ha pedido que batten *lea* el vault. batten proyecta
  procedimiento hacia afuera; leer notas del usuario es un producto distinto.
- El costo es alto: servidor persistente, invalidación de caché, y un modo de falla nuevo
  (¿qué hace el gate si el servidor de vault no responde?) justo en el componente cuyo problema
  principal hoy es fallar en silencio (§4.2).
- **Lo que sí falta y es barato:** que la nota del vault muestre los dos veredictos (§4.4), y
  documentar que los `.base` no se pueden leer por script — son vistas humanas, SQLite sigue
  canónico.

---

## 8. El resto de los hallazgos confirmados

52 confirmados, 8 corregidos. Los 44 restantes están en
[`../field-test/verified.json`](../field-test/verified.json), cada uno con repro, evidencia y un
`fix_hint` con file:line. Agrupados por causa:

### 8.1 — Honestidad de superficie (la familia del principio #1)

| # | qué | costo |
|---|---|---|
| 31 | `measure` omite los buckets de caché: subestima hasta 21,9× | S |
| 32 | `measure` imprime `$0.00` para un modelo sin precio: presenta lo desconocido como gratis | S |
| 33 | `budget`/`runs` presentan el total imputado como completo cuando parte del run no tiene precio | M |
| 10 | un override es invisible en todo el CLI, y `show` afirma lo contrario de la verdad | M |

Los cuatro son la misma falla: reportar como sabido algo que no se sabe. Van juntos.

### 8.2 — Ciclo de vida

| # | qué | costo |
|---|---|---|
| 12 | `check` sobre un unit cerrado bifurca en silencio un segundo run sin ancla | S |
| 21 | los ids de unit nunca se validan contra `unit.pattern`: un typo abre un run fantasma permanente | S |
| 23 | los runs cerrados son inalcanzables desde el CLI (`show --run <id>` descarta la bandera) | S |
| 19 | el aviso de run rancio nunca se puede limpiar: `events.run_id` es siempre NULL | S |
| 24 | entrar a una fase con `diff_from: anchor` sin ancla es completamente silencioso | S |

### 8.3 — Presentación

Menores con arreglo de una línea: exit codes de Windows como basura sin signo (#13), solapamiento
de tarjetas en el canvas (#45), `batten tui` con stdout no-terminal entra a alt-screen y no sale
(#46), `'1 runs'` (#48), `%.1fM` que muestra 42,6k como `0.0M` (#36), `init --help` escribe el
archivo en vez de imprimir ayuda (#54), `init --from` no lee el documento (#0).

---

## 9. La TUI y el seguimiento de cumplimiento

**Hoy la TUI sigue *runs*, no cumplimiento.** Dos razones de naturaleza distinta.

**Ya corregido (`24d7cd2`):** cargaba solo `LatestVerdict`, así que `batten check` —que siempre
escribe último— tapaba la evidencia del revisor. Ahora muestra los dos y **nombra la mitad
ausente**.

**Estructural, pendiente:** no existe modelo de criterios de aceptación. `evidence` es un
`[]string` plano; la palabra "criterios" aparece 10 veces en prosa del código y **cero veces como
dato**. Y `unit.plan` / `unit.locator` —el backlog que `init` se esfuerza en encontrar— tienen
**un solo consumidor**: `internal/mcp/mcp.go:794`, que los copia de vuelta en `batten_spec`.

**Propuesta en tres fases:**

- **A (~1 día)** — leer el backlog. Un `internal/plan` que resuelva `unit.plan` + `unit.locator` a
  una lista de units con su bloque de texto. Es parseo de markdown por encabezado; el locator ya
  está en el spec.
- **B (~2 días)** — criterios como dato. Extraer del bloque del unit los ítems (viñetas /
  checkboxes) a una tabla `criteria(run_id, unit_id, idx, text, status)`, y extender el envelope
  para que `evidence` pueda citar un criterio por índice **sin romper el formato actual**.
  *Cuidado:* el hallazgo #27 ya mostró que pasar objetos donde se esperan strings escupe un error
  de Go crudo. Cualquier extensión tiene que aceptar las dos formas y fallar con mensaje humano.
- **C (~1 día)** — la vista. En la TUI, un pane de backlog:
  `US-001 ✓ · US-002 ✓ · US-003 ◐ · US-004 ·`. En el CLI, `batten status`.

**Conexión con la literatura:** el benchmark AFTER
([arXiv:2606.23127](https://arxiv.org/abs/2606.23127)) muestra que refinar la memoria
procedimental mejora el desempeño de forma medible (3,7–6,7 puntos). batten hoy **impone**
proceso pero no **aprende** de las corridas cuáles funcionan. La fase B es el prerrequisito de
datos para eso: sin criterios como dato, no hay señal de qué proceso produjo qué resultado.

---

## 10. Orden propuesto

**Antes de construir nada encima:**

| | qué | § | por qué primero |
|---|---|---|---|
| 1 | el tercer sitio del un-solo-veredicto | 4.4 | inconsistencia conocida, 30 min |
| 2 | validar los 8 fixes contra la réplica de proyecto_ui | 4.3 | lo único que puede invalidar trabajo hecho |
| 3 | rediseñar el payload MCP (`content` compacto / `structuredContent` completo) | 4.1 | 70–90 % de ahorro de contexto |
| 4 | fail-open ruidoso en el camino de excepción del hook | 4.2 | el silencio es indistinguible de la aprobación |

**Después, por valor:**

| | qué | § |
|---|---|---|
| 5 | `scan-diff` post-fan-out (red de seguridad + dato de sobre-declaración) | 5.1 pto. 4 |
| 6 | la cadena graphify→engram en `/batten-build` | 5.3 |
| 7 | parseo de Bash para el write-set guard, advisory primero | 5.1 pto. 1–3 |
| 8 | claim de directorio y colisión entre runs | 5.2 |
| 9 | criterios como dato + vista de cumplimiento | 9 |
| 10 | la familia de honestidad de superficie | 8.1 |
| 11 | decidir campo por campo: implementar o sacar | 6 |
| 12 | ciclo de vida y presentación | 8.2, 8.3 |

**Descartado en este ciclo:** TOON (§7.2) · migración a OCC (§7.1) · Obsidian bidireccional (§7.3).

---

## 11. Lo que este plan NO resuelve

- **batten nunca fue adoptado por un proyecto ajeno**, con gente que no lo escribió. El field
  test usó agentes que no lo conocían —lo más cerca posible sin usuarios reales— pero no es lo
  mismo.
- **No hay release taggeado.** El camino de release está verificado leyéndolo de punta a punta,
  no ejecutándolo.
- **El formato de transcript que batten parsea no es una API pública** y puede cambiar sin aviso.
  Cuando se rompe, batten reporta el conteo como no disponible en vez de adivinar — correcto,
  pero significa que el ledger puede quedarse ciego sin previo aviso.
- **La comparación de mercado no está verificada de forma independiente.** El estudio externo
  nombra plugins concretos (`rpw-published`, `superpowers`, `grainulator`, `harness-claude`,
  `claude-obsidian`, `graph-context-for-claude-code`, `claude-canvas`) con descripciones
  funcionales detalladas. **No verifiqué ninguna de esas descripciones contra su código.** Si el
  posicionamiento competitivo va a sostener una decisión, hay que hacerlo.

---

## Fuentes

Papers (ventana junio–julio de 2026, más tres de mayo que resultaron load-bearing):

1. [Reason Less, Verify More: Deterministic Gates Recover a Silent Policy-Violation Failure Mode in Tool-Using LLM Agents](https://arxiv.org/html/2607.07405v1) — arXiv:2607.07405, 8 jul 2026
2. [CoAgent: Concurrency Control for Multi-Agent Systems](https://arxiv.org/abs/2606.15376) — arXiv:2606.15376, 13 jun 2026
3. [Managing Procedural Memory in LLM Agents: Control, Adaptation, and Evaluation](https://arxiv.org/abs/2606.23127) — arXiv:2606.23127, 22 jun 2026
4. [A Deterministic Control Plane for LLM Coding Agents](https://arxiv.org/html/2606.26924v1) — arXiv:2606.26924, jun 2026
5. [ActPlane: Programmable OS-Level Policy Enforcement for Agent Harnesses](https://arxiv.org/html/2606.25189v2) — arXiv:2606.25189, jun 2026
6. [Autoformalization of Agent Instructions into Policy-as-Code](https://arxiv.org/pdf/2606.26649) — arXiv:2606.26649, jun 2026
7. [Neural Procedural Memory: Empowering LLM Agents with Implicit Activation Steering](https://arxiv.org/abs/2606.29824) — arXiv:2606.29824, jun 2026
8. [S-Bus: Automatic Read-Set Reconstruction for Multi-Agent LLM State Coordination](https://arxiv.org/pdf/2605.17076) — arXiv:2605.17076, may 2026
9. [Notation Matters: A Benchmark Study of Token-Optimized Formats in Agentic AI Systems](https://arxiv.org/html/2605.29676v1) — arXiv:2605.29676, 28 may 2026
10. [Reframing LLM Agent Security as an Agent–Human Interaction Problem](https://arxiv.org/pdf/2605.24309) — arXiv:2605.24309, may 2026

Fuentes web:

11. [MCP structuredContent: How to Return Large Results Without Flooding the Context Window](https://futuresearch.ai/blog/mcp-results-widget/)
12. [The 2026-07-28 MCP Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
13. [Claude Code Plugins: Inside Anthropic's Official Directory (2026 Guide)](https://claudecamp.ai/blog/claude-code-plugins-official-directory)
