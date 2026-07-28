# Plan de mejora

> Estado base: commit `24d7cd2`, versión `0.1.0`, suite verde, sin release. Escrito 2026-07-28
> después del field test y de sus 8 correcciones. Cada punto sale de evidencia —un hallazgo
> verificado, una medición, o un hueco confirmado leyendo el código— y dice de dónde.
>
> Contexto del plugin: [`plugin_al_momento.md`](plugin_al_momento.md).
> Reporte del field test: [`../FIELD-TEST.md`](../FIELD-TEST.md).

---

## Resumen de decisiones

| tema | decisión | por qué |
|---|---|---|
| **TOON** en vez de JSON | **descartado** | medido: +5.7 % de tokens en el conjunto real de payloads |
| **duplicación de payload MCP** | **investigar — prioridad alta** | cada respuesta va **dos veces**; eso es 2×, no ±20 % |
| **TUI: seguimiento de cumplimiento** | **sí, hacerlo** | hoy sigue *runs*, no cumplimiento; el dato de criterios no existe |
| **Validar contra proyecto_ui** | **sí, y es el hueco real** | los 8 fixes nunca se revalidaron contra la forma difícil |
| **Cadena graphify → engram** | **sí, y no estaba considerada** | el subagente del fan-out no consulta nada |

---

## 0. Deuda inmediata que dejé abierta

Antes de cualquier mejora, una inconsistencia de mi propio trabajo de esta sesión.

### 0.1 — `export.Run` sigue mostrando un solo veredicto

**Evidencia:** `internal/export/export.go:58` → `st.LatestVerdict(run.RunID, "")`.

Enseñé a `batten show` (`92ae1cb`) y a la TUI (`24d7cd2`) a mostrar **ambos** veredictos. El
tercer consumidor —el que escribe la nota de Obsidian y el canvas— quedó leyendo solo el último.
Como `batten check` siempre escribe al final, la nota del vault tapa la evidencia del revisor con
la salida de checks.

**Arreglo:** dos líneas, idénticas a las otras dos: `LatestVerdictBySource(...,"batten")` +
`LatestVerdictNotBySource(...,"batten")`, y que `canvas.Render` reciba ambos.

**Costo:** ~30 min con test. **Hacerlo primero**, porque es una inconsistencia conocida.

---

## 1. TOON — descartado, con la medición que lo sostiene

**La pregunta:** ¿conviene TOON en vez de JSON para lo que batten le manda al LLM?

**Cómo lo medí:** capturé los payloads reales del servidor MCP contra la base del demo y los
conté con `tiktoken` (`o200k_base`). No es el tokenizador de Claude, así que los números son
**comparativos**, no absolutos — pero la dirección y el orden de magnitud son válidos.

| herramienta MCP | JSON | TOON | |
|---|---:|---:|---:|
| `batten_runs` | 344 | 270 | **−21.5 %** |
| `batten_run_graph` | 416 | 490 | +17.8 % |
| `batten_verdict_status` | 260 | 267 | +2.7 % |
| `batten_spec` | 430 | 505 | +17.4 % |
| **total** | **1450** | **1532** | **+5.7 % peor** |

**Por qué.** TOON gana donde hay **tabla**: filas uniformes que pagan los nombres de columna una
vez en vez de una por fila. En `batten_runs` eso escala bien (−22 % con 3 runs, −36 % con 100).
Pero `run_graph` y `spec` son **árboles anidados heterogéneos**, y ahí la indentación cuesta más
que las llaves. Como batten devuelve mayoritariamente lo segundo, adoptarlo en bloque *sube* el
costo.

> **Nota metodológica:** mi primera medición dio TOON peor en todo. Era mi encoder: las filas de
> `runs` llevan un `verdict` anidado y el tabulador las rechazaba, cayendo a la forma verbosa.
> Un encoder real aplana esa columna. El cuadro de arriba es el corregido. Vale la pena decirlo
> porque es exactamente el error que haría descartar el formato por la razón equivocada.

**Decisión: no adoptar.** Reevaluar solo si aparece un payload genuinamente tabular y grande —
por ejemplo un `batten_runs` histórico de 50+ filas, o un futuro export del ledger de usage.

### 1.1 — El hallazgo que salió midiendo esto: el payload va DOS VECES

**Evidencia medida:**

| herramienta | `content[].text` | `structuredContent` | idénticos |
|---|---:|---:|:--:|
| `batten_runs` | 932 chars | 932 chars | sí |
| `batten_run_graph` | 1176 | 1176 | sí |
| `batten_verdict_status` | 885 | 885 | sí |
| `batten_spec` | 1466 | 1466 | sí |

El SDK de MCP emite ambos cuando la herramienta declara output schema: `structuredContent` para
clientes nuevos y `content[].text` como compatibilidad. **Byte por byte lo mismo.**

Eso es un factor **2×**, que empequeñece cualquier discusión de ±20 % de formato.

**Qué falta antes de tocar nada:** confirmar si Claude Code mete las dos copias en el contexto o
descarta una. Si mete las dos, hay un ~50 % de ahorro sin cambiar ni un formato ni una API.

**Cómo medirlo:** un turno con una llamada a `batten_run_graph` y `/context` antes y después, o
inspeccionar el transcript de la sesión. **Costo: 1 hora.** **Prioridad: la más alta de este
documento**, por relación ahorro/esfuerzo.

---

## 2. La TUI — sí, vale la pena, y hoy no sigue cumplimiento

**La pregunta:** ¿la TUI da seguimiento completo al cumplimiento de las tareas?

**Respuesta: no**, y por dos razones de naturaleza distinta.

### 2.1 — Lo que ya se corrigió (`24d7cd2`)

La TUI cargaba solo `LatestVerdict`. Como `batten check` escribe último, tapaba la evidencia del
revisor con su propia salida de checks: un veredicto verde en pantalla que cumplía **media**
regla. Ahora muestra los dos y **nombra la mitad ausente**.

### 2.2 — Lo estructural: no existe modelo de criterios de aceptación

**Evidencia:** `evidence` es un `[]string` plano (`internal/store/store.go`, `Verdict.Evidence`).
La palabra "criterios" aparece 10 veces en prosa del código y **cero veces como dato**.

La TUI no puede mostrar "3 de 5 criterios cumplidos" porque ese dato no se guarda en ningún lado.

### 2.3 — El backlog se detecta y después se ignora

**Evidencia:** `unit.plan` y `unit.locator` tienen **un solo** consumidor en todo el código:
`internal/mcp/mcp.go:794`, que los copia de vuelta en `batten_spec`. Nadie los lee para nada más.

Es decir: `init` hace el trabajo difícil de encontrar el backlog y deducir el patrón de
encabezado (`### {id}`), lo escribe en el spec... y ninguna superficie lo usa. No hay vista de
"qué US están hechas y cuáles faltan".

### 2.4 — Propuesta

**Fase A — leer el backlog (habilita todo lo demás).**
`store` o un paquete nuevo `internal/plan` que resuelva `unit.plan` + `unit.locator` a una lista
de units con su bloque de texto. Es parsing de markdown por encabezado; el locator ya está en el
spec.

**Fase B — criterios como dato.**
Extraer del bloque del unit los ítems de criterio (viñetas / checkboxes) y guardarlos en una
tabla `criteria(run_id, unit_id, idx, text, status)`. Extender el envelope de veredicto para que
`evidence` pueda citar un criterio por índice, **sin romper el formato actual** — hoy es
`["texto", ...]` y debería seguir aceptándolo.

> Cuidado de compatibilidad: el hallazgo #27 del field test ya mostró que pasar objetos donde se
> esperan strings escupe un error de Go crudo. Cualquier extensión del envelope tiene que aceptar
> las dos formas y fallar con un mensaje humano.

**Fase C — la vista.**
En la TUI: una pestaña o pane de *backlog* con `US-001 ✓ · US-002 ✓ · US-003 ◐ · US-004 ·`, y en
el detalle del run, los criterios con su estado. En el CLI: `batten status` o `batten runs --plan`.

**Costo:** A ~1 día · B ~2 días (el diseño del envelope es lo delicado) · C ~1 día.
**Prioridad:** alta. Es la diferencia entre "batten sabe qué corrió" y "batten sabe qué se cumplió".

---

## 3. Validar contra proyecto_ui — el hueco real

**La pregunta:** ¿probaste la versión de proyecto_ui contra un proyecto nuevo?

**Respuesta honesta: no en esta sesión.** Lo digo explícito porque importa.

- La réplica de proyecto_ui se usó en la **corrida original** (sesión anterior); ese sandbox ya
  no existe.
- La verificación de los 63 hallazgos y los 8 fixes se hicieron sobre **sandboxes sintéticos** y
  sobre `taskly`, el demo desde cero.
- Por lo tanto **los 8 fixes nunca se revalidaron contra la forma difícil**, que es justamente
  donde varios de los hallazgos aparecieron.

### 3.1 — Por qué las dos formas no son equivalentes

| | `taskly` (demo nuevo) | réplica de proyecto_ui |
|---|---|---|
| repo git | sí | **no lo es** — todo el modelo cuelga de git |
| código | sí, Go con tests | **ninguno**: 4 dominios con solo su `AGENTS.md` |
| dominios | 2 | 4 |
| backlog | 5 items | 40+ `US-0NN` |
| skills / agentes | 0 | 47 skills, 9 agentes custom |
| build files | `Makefile` | ninguno → `gates.checks` vacío |

Las dos últimas filas son las que muerden: **un repo sin build files cae en la rama del gate sin
checks**, que es exactamente donde vivía la regresión de `on_exceed: block`. Y `deriveDomains`
toma un camino distinto cuando un dominio no tiene código.

### 3.2 — Propuesta

Reconstruir la réplica en sandbox (nunca el original, nunca sin `BATTEN_DB` exportado) y correr
contra ella una matriz corta, apuntando a lo que los fixes tocaron:

1. `init` sobre 4 dominios sin código y 40+ items de backlog → ¿unit `US-\d{3}`, dominios
   completos, `gates.checks` vacío reportado como vacío?
2. Gate **sin checks** + presupuesto excedido → debe DENEGAR (la regresión de `24d8e4c`).
3. Dos units abiertos en la misma fase → los dos canvas intactos (ids por run).
4. Commit con `git -c user.name=... commit` → debe entrar al gate.
5. Primer commit tras adoptar → debe **avisar**, no callar.
6. Commit cuyo mensaje nombra otro unit → debe denegar.
7. `ingest` con transcript previo al run → debe reportar lo descartado.
8. Repo **sin git init** → ¿degrada con aviso o revienta?

El punto 8 no está cubierto por ningún test hoy y es el estado literal de proyecto_ui.

**Costo:** ~medio día. **Prioridad: la más alta junto con 1.1.** Es la única de las mejoras que
puede *invalidar* trabajo ya hecho, así que va antes de construir encima.

---

## 4. La cadena graphify → engram — no estaba considerada

**La pregunta:** ¿consideraste que el subagente pregunte a graphify y, si no encuentra, a engram?

**Respuesta: no, y es un hueco real.** Verificado leyendo el código y los comandos.

### 4.1 — Qué existe hoy

| dónde | qué hace |
|---|---|
| `/batten-plan` | usa `graphify god-nodes --json` y `graphify affected "X"`, y `mem_search` antes de planear |
| `/batten-verify` | `mem_search "<el bug>"` antes de juzgar |
| `/batten-close` | escribe la decisión a engram |
| **`/batten-build`** | **nada.** Cero menciones de graphify o engram |

**El subagente del fan-out —el que efectivamente escribe código— no consulta nada.** La
orientación ocurre solo en el orquestador, durante el plan.

### 4.2 — Y la declaración que lo pediría no la lee nadie

| campo | promete | consumidores |
|---|---|---|
| `capabilities.graph.query_before_read` | preguntar al grafo antes de leer | **0** |
| `phases[].graph_query` | consultar el grafo en vez de grepear | **0** |

Ambos se escriben en el `batten.yaml` que genera `init`, aparecen en README y DESIGN, y **no
gobiernan nada**. Es la misma clase que `retry_of` (4 consumidores, 0 productores) del hallazgo
#41: el usuario lo escribe, asume que rige, y no rige.

### 4.3 — Por qué la cadena tiene sentido

El orden no es cosmético, corresponde a las tres memorias:

1. **graphify** responde *qué es* el código: quién llama a esto, qué se rompe si lo cambio.
   Barato, exacto, sin API key para el modo `--code-only`.
2. **engram** responde *qué decidimos*: si esto ya se resolvió, por qué se hizo así, qué falló
   la vez pasada.
3. **grep / leer archivos** es el fallback, y es el más caro en tokens de los tres.

Hoy el subagente arranca directo en el paso 3.

### 4.4 — Propuesta

**Paso 1 — hacer que la declaración signifique algo (barato, alto valor).**
Que `/batten-build` incluya en el prompt de cada subagente, cuando `query_before_read: true`:

> Antes de leer archivos para orientarte, preguntá al grafo:
> `graphify affected "<símbolo>"` para el radio de impacto, `graphify query "<qué maneja X>"`
> para ubicar. Si el grafo no responde o no está instalado, `mem_search "<el problema>"` por si
> ya se resolvió. Solo entonces leé archivos. Si ninguno de los dos está, decilo en tu retorno
> en vez de simular que consultaste.

Esa última frase importa: es el mismo principio #3 —fallar-abierto en voz alta— aplicado a la
orientación.

**Paso 2 — que `doctor` valide la coherencia.**
Si `query_before_read: true` pero graphify no está en PATH, o si `graph_query: true` en una fase
sin proveedor de grafo, avisar. Hoy `doctor` no cruza esos dos datos.

**Paso 3 — medirlo, no creerlo.**
batten ya marca cada run con si el grafo estaba fresco (`SetCodeGraph`) y ya cuenta tokens por
nodo. `measure` puede comparar el costo de orientación de subagentes con y sin grafo. Si no
ahorra, hay que decirlo — es el mismo trato que se le da a headroom: *admitido, pero medido*.

**Costo:** paso 1 ~2 h (es prompt) · paso 2 ~2 h · paso 3 ~medio día.
**Prioridad:** alta para el paso 1 — es la mejora con mejor relación esfuerzo/impacto del
documento, y cierra una promesa que el spec ya hace.

### 4.5 — Obsidian: qué falta

El vault funciona (nota del run, canvas, dashboards, disparado desde `Stop`, `canvas` y post-veredicto).
Pendientes menores, todos del field test:

- La nota hereda el bug de un-solo-veredicto (§0.1).
- Los `.base` no se pueden leer por script — son vistas humanas. SQLite sigue canónico. Esto es
  correcto y solo hace falta que esté documentado donde alguien lo vaya a buscar.

---

## 5. El resto de los hallazgos confirmados

52 confirmados, 8 corregidos. Los 44 restantes están en
[`../field-test/verified.json`](../field-test/verified.json), cada uno con repro, evidencia y un
`fix_hint` con file:line. Agrupados por causa:

### 5.1 — Honestidad de superficie (la familia del principio #1)

| # | qué | costo |
|---|---|---|
| 31 | `measure` omite los buckets de caché: subestima hasta 21.9× | S |
| 32 | `measure` imprime `$0.00` para un modelo sin precio: presenta lo desconocido como gratis | S |
| 33 | `budget`/`runs` presentan el total imputado como completo cuando parte del run no tiene precio | M |
| 10 | un override es invisible en todo el CLI, y `show` afirma lo contrario de la verdad | M |

Los cuatro son la misma falla: reportar como sabido algo que no se sabe. Van juntos.

### 5.2 — El write-set guard tiene dos huecos

| # | qué | costo |
|---|---|---|
| 6 | un agente al que se le negó un `Edit` hace la misma escritura por `Bash` (`>`, `sed -i`, heredoc) sin aviso | M |
| 7 | un claim de **directorio** se acepta, se reporta como protector, y no cerca nada | S |
| 4 | `claim` no mira otros runs abiertos: dos sesiones creen poseer el mismo archivo, y después las deniega a ambas | M |

El #6 es el más serio: la valla existe y se puede rodear con una herramienta que el propio agente
ya tiene. No es un fail-open deliberado — la atribución **funciona** ahí (batten nombra al dueño
una llamada antes) y aun así no dice nada.

### 5.3 — Ciclo de vida

| # | qué | costo |
|---|---|---|
| 12 | `check` sobre un unit cerrado bifurca en silencio un segundo run sin ancla | S |
| 21 | los ids de unit nunca se validan contra `unit.pattern`: un typo abre un run fantasma permanente | S |
| 23 | los runs cerrados son inalcanzables desde el CLI (`show --run <id>` descarta la bandera) | S |
| 19 | el aviso de run rancio nunca se puede limpiar: `events.run_id` es siempre NULL | S |
| 24 | entrar a una fase con `diff_from: anchor` sin ancla es completamente silencioso | S |

### 5.4 — Declarado y no leído

`diff_from` · `graph_query` · `query_before_read` · `models.*` · `provenance.format` · `retry_of`.

**Decisión de fondo pendiente:** para cada uno, implementarlo o **sacarlo del spec**. Un campo
que el usuario escribe creyendo que gobierna y no gobierna es peor que su ausencia. Mi
recomendación: implementar `query_before_read` y `graph_query` (§4), implementar `retry_of`
(barato y hace legible el fan-out con reintentos), y **sacar** `models.*` y `provenance.format`
del spec generado hasta que existan, dejándolos documentados como futuros.

### 5.5 — Presentación

Menores con arreglo de una línea: exit codes de Windows como basura sin signo (#13), solapamiento
de tarjetas en el canvas (#45), `batten tui` con stdout no-terminal entra a alt-screen y no sale
(#46), `'1 runs'` (#48), `%.1fM` que muestra 42.6k como `0.0M` (#36), `init --help` escribe el
archivo en vez de imprimir ayuda (#54).

---

## 6. Orden propuesto

**Antes de construir nada encima:**

| | qué | por qué primero |
|---|---|---|
| 1 | §0.1 — el tercer sitio del un-solo-veredicto | inconsistencia conocida, 30 min |
| 2 | §3 — validar los 8 fixes contra la réplica de proyecto_ui | es lo único que puede invalidar trabajo hecho |
| 3 | §1.1 — medir si el payload MCP duplicado se cobra dos veces | mejor relación ahorro/esfuerzo del documento |

**Después, por valor:**

| | qué |
|---|---|
| 4 | §4 paso 1 — la cadena graphify→engram en `/batten-build` |
| 5 | §5.2 — los dos huecos del write-set guard (#6 sobre todo) |
| 6 | §2 — criterios como dato, y la vista de cumplimiento |
| 7 | §5.1 — la familia de honestidad de superficie |
| 8 | §5.4 — decidir campo por campo: implementar o sacar |
| 9 | §5.3 y §5.5 — el resto |

**Descartado:** §1 TOON.

---

## 7. Lo que este plan NO resuelve

Dicho explícito para que nadie lo dé por hecho:

- **batten nunca fue adoptado por un proyecto ajeno**, con gente que no lo escribió. El field
  test usó agentes que no lo conocían, que es lo más cerca que se puede llegar sin usuarios
  reales — pero no es lo mismo.
- **No hay release taggeado.** El camino de release está verificado leyéndolo de punta a punta,
  no ejecutándolo.
- **El formato de transcript que batten parsea para contar tokens no es una API pública** y puede
  cambiar sin aviso. Cuando se rompe, batten reporta el conteo como no disponible en vez de
  adivinar — que es lo correcto, pero significa que el ledger puede quedarse ciego sin previo
  aviso.
