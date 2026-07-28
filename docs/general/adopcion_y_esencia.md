# Adopción y esencia

> Evaluación de un estudio de mercado pesimista que proyecta 100–300 estrellas para batten y
> propone cinco cambios estratégicos. Escrito 2026-07-28. La pregunta que responde:
> **¿esos cambios le quitan la esencia al plugin?**
>
> Respuesta corta: **tres de los cinco no la tocan, uno la mejora, y solo dos sub-ítems concretos
> la destruyen.** Pero el diagnóstico que sostiene el estudio está mal, y corregirlo cambia qué
> hay que hacer.

---

## 1. Las cifras de referencia son reales

Lo primero fue verificarlas, porque si no lo fueran la comparación entera se caía. No es el caso:

| proyecto | estrellas | qué es |
|---|---:|---|
| [superpowers](https://github.com/obra/superpowers) | **~224.700** | framework de skills que impone metodología |
| [graphify](https://github.com/Graphify-Labs/graphify) | **~97.500** | grafo de conocimiento del código |
| [caveman](https://github.com/juliusbrussee/caveman) | **~54.000** | comprime tokens de salida ~65 % |

Son reales y son enormes. La comparación merece tomarse en serio.

---

## 2. Pero el modelo pesimista es aritméticamente inconsistente

El estudio propone:

```
S = A_act × F_dev × (1 − R_fric) × R_conv
S = 50.000 × 0,10 × 0,10 × 0,05 = 25 estrellas
```

**El error está en `A_act ≈ 50.000`.** Se justifica con "56.498 plugins indexados y 24.614
creadores". Eso es **oferta**, no demanda: cuenta cuántos plugins existen, no cuántos
desarrolladores podrían instalar el tuyo. Es como estimar el mercado de un restaurante contando
restaurantes.

Y el propio conjunto de comparación lo refuta: **caveman tiene ~54.000 estrellas.** Con el
`R_conv = 0,05` que el mismo modelo asume, eso implica **~1,08 millones de usuarios activos**. El
`A_act` del modelo está equivocado por un factor de al menos **20×**.

Corrigiendo solo ese parámetro y dejando todo lo demás igual de pesimista:

```
S = 1.000.000 × 0,10 × 0,10 × 0,05 = 500 estrellas
```

Y `F_dev = 0,10` ("solo los ingenieros de plataforma se interesan") también es discutible —
§3 explica por qué.

**No estoy diciendo que batten vaya a tener 50.000 estrellas.** Estoy diciendo que el número 25
no sale de un análisis válido, y que planear sobre él lleva a decisiones equivocadas: te empuja a
cambios drásticos de identidad cuando el problema real es otro y es más barato.

---

## 3. La premisa central del estudio está refutada por el #1 del ecosistema

El estudio argumenta:

> "el desarrollador promedio percibe las barandillas estrictas como un obstáculo a su velocidad
> individual […] A los desarrolladores no les gusta que les pongan límites o que un plugin les
> bloquee un commit."

**El plugin más estrellado del ecosistema de Claude Code —224.700 estrellas— es literalmente un
imponedor de disciplina.** [superpowers](https://github.com/obra/superpowers) empaqueta skills
que los agentes siguen **como flujos obligatorios**: la skill de TDD *impone*
RED-GREEN-REFACTOR (escribí el test que falla, miralo fallar, escribí el mínimo, miralo pasar,
commiteá), y la de brainstorming exige refinar la idea **antes de escribir código**.

Eso es restricción pura. Y es el proyecto número uno.

**Segundo contraejemplo, aún más cercano a batten:** graphify, con 97.500 estrellas, tiene como
punto de venta central *decir lo que no sabe*. Su README:

> Cada conexión está etiquetada `EXTRACTED` (explícita en la fuente) o `INFERRED` (resuelta por
> graphify), así podés distinguir lo que se leyó directo de lo que se infirió.
>
> **No es un índice vectorial.** Sin embeddings, sin vector store: un grafo real que recorrés.

Eso es exactamente el **principio #1 de batten** —nunca reportar un número que no se tiene—
aplicado a las aristas de un grafo. Y no le costó adopción: se la dio.

**Tercer dato, el más útil de todos:** graphify **también bloquea**. Tiene
`GRAPHIFY_HOOK_STRICT=1`, que bloquea la primera lectura cruda de un archivo y redirige al grafo.
Pero es **opt-in y viene apagado**; el default es un "empujón suave".

**batten ya hace exactamente eso** — `batten init` arranca siempre en `enforcement: report`, donde
las compuertas avisan y no bloquean. Lo que no hace es **liderar con eso**. El README abre con
"El producto son dos negaciones".

> **Conclusión de esta sección:** ni la restricción ni la honestidad son la barrera. Los dos
> proyectos más grandes del ecosistema hacen ambas cosas. El problema de batten es otro.

---

## 4. Lo que sí distingue a los proyectos de 50k del resto

Comparando los tres verificados contra batten, aparecen tres diferencias reales — y ninguna
requiere tocar la arquitectura.

### 4.1 — Tiempo hasta el primer valor

| proyecto | pasos hasta que ves algo | qué ves |
|---|---|---|
| caveman | 1 comando | 65 % menos tokens, inmediato |
| graphify | 2 comandos + 1 | un grafo interactivo en el navegador |
| superpowers | 2 comandos | la metodología ya activa |
| **batten** | **~8 pasos** | **un commit denegado** |

El recorrido de batten hoy: instalar → `init` → editar el YAML → llenar invariantes → cambiar a
`enforce` → `phase` → trabajar → `verdict` → `check` → commit. Y **la primera cosa que el plugin
hace por vos es negarte algo.**

Esa es la diferencia que importa. No es que restrinja: es que la restricción llega **antes** que
el beneficio. superpowers también restringe, pero su primera experiencia es "ahora tenés 20
skills que te guían", no "no podés commitear".

### 4.2 — Un artefacto que se pueda mostrar

El `graph.html` de graphify es una captura de pantalla que se comparte sola: nodos coloreados por
comunidad detectada, clickeable, se abre en cualquier navegador. Eso es lo que hace que la gente
lo postee.

batten produce un `.canvas` que **requiere Obsidian** y que solo se escribe **al final de la
sesión** (`Stop`). Un usuario sin Obsidian nunca ve nada visual.

### 4.3 — Un número medido en el titular

caveman: *"corta el 65 % de los tokens"*. Medido, con rango publicado (22–87 %).

batten: *"memoria procedimental como datos"*. Es preciso y es abstracto.

**Y acá está lo interesante: batten ahora tiene su número, y no es autorreportado.** El paper
[Reason Less, Verify More](https://arxiv.org/html/2607.07405v1) (arXiv:2607.07405, 8 jul 2026)
mide exactamente el mecanismo de batten:

- **78 %** de las fallas de agentes son silenciosas — el agente reporta éxito con el estado corrupto
- compuertas deterministas: éxito de **29,6 % → 42,0 %** (+12,4 pp, replicado con P = 0,0008)
- fiabilidad pass₅: **8 % → 26 %** — más del triple

Eso es un titular con números de terceros:

> **Los agentes fallan en silencio el 78 % de las veces. Las compuertas deterministas triplican
> la fiabilidad. batten es una.**

---

## 5. Evaluación de los cinco cambios propuestos, contra la esencia

Primero, qué **es** la esencia — los siete principios que están en el código, no en el marketing:

1. SQLite es canónico; todo lo demás es proyección con pérdida
2. Nunca reportar un número que no se tiene
3. Fallar abierto **solo en voz alta**
4. Un aviso nunca gana a una denegación
5. Dos veredictos, de dos productores distintos
6. El binario hace el trabajo del hook (sin bash, sin jq)
7. Un id de nodo que no lleva su run no es un identificador

Ahora los cinco cambios:

### ✅ #1a — Reposicionar de "guardia" a "habilitador de trabajo en paralelo"

**Adoptar. Cuesta cero líneas de código.** El write-set guard *ya es* lo que hace seguro el
fan-out; decirlo así es describirlo con precisión, no maquillarlo. Que cuatro subagentes escriban
en paralelo sin pisarse es la función; "bloquear" es el mecanismo. Un README puede liderar con la
función.

**No toca ningún principio.**

### ❌ #1b — Concurrencia optimista con autorreparación por LLM

**Rechazar en la forma propuesta.** El estudio sugiere que ante un conflicto, batten "utiliza al
propio modelo para evaluar el conflicto semántico y autorreparar la integración".

Eso **viola el principio #5 y la razón de existir del proyecto**. batten existe porque un agente
que se autoevalúa aprueba trabajo roto — eso está medido en el paper de §4.3, con el 78 % de
fallas silenciosas. Poner al modelo a juzgar si su propio conflicto de escritura "importa" es
reintroducir exactamente el fallo que el gate mata, en la otra mitad del producto.

**Lo que sí se puede adoptar de [CoAgent](https://arxiv.org/abs/2606.15376)** (arXiv:2606.15376,
13 jun 2026) es su principio, que batten ya tiene el vocabulario para expresar:

> el control se vuelve **advisory** — el runtime informa y el agente repara

batten ya distingue `deny()` de `advise()`. Extender `advise()` a colisiones de baja severidad es
barato y está alineado. Lo que **no** hay que hacer es que el modelo decida si una colisión es de
baja severidad.

**Y falta el dato previo:** batten no tiene ninguna medición de cuánto se bloquean los subagentes
en la práctica. [S-Bus](https://arxiv.org/pdf/2605.17076) aporta el número que decide si el
problema existe — los agentes **sobre-declaran su uso de recursos entre 32 % y 49 %**. Si batten
sobre-declara parecido, el bloqueo es real y vale medirlo. Medir primero, rediseñar después.

### ✅ #2 — Eliminar la fricción de configuración

**Adoptar, y es lo más urgente de los cinco.** Conecta directo con §4.1.

**No toca ningún principio** — el YAML sigue siendo la declaración canónica; lo que cambia es que
dejás de exigir que se escriba a mano antes de que el plugin sirva para algo.

batten ya está a mitad de camino: `init` arranca en `report` y genera un spec funcional. Lo que
falta es que **el camino de cero a valor no pase por editar un archivo**.

### ✅ #3 — Efecto visual en vivo

**Compatible con la esencia, pero caro, y hay una versión barata que da el 80 %.**

Un dashboard web con WebSockets es un servidor persistente, invalidación de caché y un modo de
falla nuevo. La versión barata: que `batten canvas` emita **también un HTML autocontenido** que se
abra en cualquier navegador sin Obsidian — que es exactamente lo que hace graphify con
`graph.html`. Y que se pueda generar **durante** la corrida, no solo en `Stop`.

**No toca ningún principio** — es una superficie de lectura sobre el mismo SQLite canónico.

### ⭐ #4 — Convertir la compuerta en un generador de Pull Requests

**Es la mejor idea del estudio, y no cuesta nada de esencia.**

El mecanismo no cambia en absoluto: el gate sigue denegando, sigue exigiendo dos veredictos,
sigue citando evidencia. Lo que cambia es **qué recibís cuando pasás**: en vez de un commit que
simplemente ocurre, un PR con la evidencia compilada, las métricas de consumo, el DAG de
subagentes, y qué criterio cubrió qué.

Invierte la valencia emocional sin tocar una sola línea del gate. La disciplina deja de ser un
peaje y pasa a ser lo que hace posible el reporte.

**Refuerza el principio #2** en vez de debilitarlo: el PR es el lugar natural para mostrar
"tokens: NO MEDIDO" con honestidad.

### ⚠️ #5 — Fail-closed y activar los campos declarados

**Mitad y mitad.**

**Activar los campos declarados: obligatorio, y por razones de esencia.** Cinco campos del spec
tienen cero consumidores (`diff_from`, `graph_query`, `query_before_read`, `models.*`,
`provenance.format`). Un plugin cuyo principio #1 es *no afirmar lo que no sabe* no puede
declarar capacidades que no tiene. Implementarlos o sacarlos, campo por campo.

**Fail-closed: rechazar.** El estudio recomienda que cualquier fallo de DB, ausencia de
configuración o panic fuerce un DENY. Eso rompe el principio #2 —*degradar, nunca romper*— de
forma concreta: SQLite bajo contención de dos sesiones devuelve `SQLITE_BUSY`, y con fail-closed
eso deniega **cada llamada a herramienta** hasta que se libere. Una herramienta de gobierno que
inutiliza la sesión el día que la base está ocupada se desinstala esa misma tarde — y encima
alimentaría exactamente los issues negativos que el estudio teme.

**La tercera opción, que es la correcta:** *fail-open ruidoso*. El commit pasa, y batten dice que
no evaluó nada:

```
batten (advertencia — no bloqueando): la compuerta NO se ejecutó para este commit.
  causa: no se pudo abrir el store (database is locked)
  este commit NO fue verificado.
```

Eso es el principio #3 aplicado a los fallos internos de batten, no solo a la atribución. Y cierra
la brecha de confianza que el estudio identifica correctamente, sin el costo que su remedio traería.

### Resumen

| # | propuesta | veredicto | costo en esencia |
|---|---|---|---|
| 1a | reposicionar como habilitador de paralelismo | **adoptar** | ninguno |
| 1b | OCC con autorreparación por LLM | **rechazar** | viola el principio #5 y la razón de existir |
| 2 | cero configuración | **adoptar** | ninguno |
| 3 | visual en vivo | **adoptar la versión barata** | ninguno |
| 4 | generador de PR | **adoptar — es la mejor** | ninguno; refuerza el #2 |
| 5a | activar campos declarados | **obligatorio** | lo exige el principio #1 |
| 5b | fail-closed | **rechazar → fail-open ruidoso** | viola los principios #2 y #3 |

**Ninguno de los cambios que hay que hacer le quita la esencia.** Los dos que sí lo harían son
justamente los dos que hay que rechazar, y ambos por la misma razón: reintroducen confianza en el
juicio del modelo justo donde batten existe para no tenerla.

---

## 6. Lo que yo agregaría, y que el estudio no propone

### 6.1 — Que la primera experiencia sea el reporte, no la negación

Es la consecuencia práctica de §4.1 y complementa el #4.

Hoy: instalás → configurás → trabajás → **te niega un commit**.
Debería ser: instalás → trabajás normal → **al final te muestra qué pasó**.

En modo `report` (el default), batten no bloquea nada: observa. Un comando —`batten report`— que
tras una sesión normal muestre qué subagentes corrieron, qué archivos tocó cada uno, cuántos
tokens gastó cada rama y qué se verificó, es valor puro sin ninguna fricción. La compuerta se
vuelve algo que **elegís encender** cuando ya confiás en lo que ves.

Eso es literalmente el patrón de graphify: nudge por default, `STRICT=1` opt-in.

### 6.2 — El ángulo de costo que nadie más cubre

Existe un ecosistema de observabilidad de costo (Langfuse, OpenTelemetry, ccusage, CloudZero),
así que "te muestro los tokens" no es diferenciador por sí solo.

**Pero todos miden dólares de API.** batten es el único que mide **porcentaje de la ventana
rodante de suscripción**, que es la métrica correcta cuando el costo marginal de un token es
cero. Un usuario de plan Max no quiere saber cuántos dólares "habría costado": quiere saber si le
va a alcanzar la cuota hasta las 5 de la tarde.

Eso ya está construido (`batten statusline` es el único sensor local de cuota que existe). Está
sin contar como diferenciador.

### 6.3 — Honestidad sobre el pronóstico

El estudio proyecta 100–300 estrellas. Corregido el error de `A_act`, con los mismos supuestos
pesimistas el número da ~500. Pero cualquier número es poco fiable: la distribución de estrellas
en GitHub es de ley de potencias y depende de un momento viral que no se planifica.

Lo que sí es predecible es el **orden relativo**:

- **Tal como está hoy** —framing de gobernanza, YAML primero, negación como primera experiencia,
  sin release taggeado, sin artefacto visual sin Obsidian— el rango de 100–300 me parece
  **realista, quizá generoso**. No por lo que hace, sino por cuánto tarda en demostrarlo.
- **Con §4.1, §4.2, §4.3 y el #4 resueltos** —valor antes que fricción, artefacto mostrable, un
  número medido de terceros en el titular, y un PR como recompensa— unos pocos miles es
  alcanzable. Superar los 10.000 requiere un momento viral que ninguna decisión técnica garantiza.

**El techo no lo pone la esencia. Lo pone el tiempo hasta el primer "ah, mirá esto".**

---

## 7. Orden sugerido, integrado con el plan técnico

Esto se combina con [`plan_mejora.md`](plan_mejora.md), que tiene el trabajo de corrección. El
orden fusionado:

| | qué | de dónde |
|---|---|---|
| 1 | el tercer sitio del un-solo-veredicto (`export.go`) | plan §4.4 |
| 2 | validar los 8 fixes contra la réplica de proyecto_ui | plan §4.3 |
| 3 | fail-open **ruidoso** (no fail-closed) | plan §4.2 / acá §5 |
| 4 | rediseñar el payload MCP (`content` compacto) | plan §4.1 |
| 5 | **`batten report`: valor antes que fricción** | acá §6.1 |
| 6 | **canvas HTML autocontenido, sin Obsidian** | acá §5 (#3) |
| 7 | **generador de PR con la evidencia compilada** | acá §5 (#4) |
| 8 | README que lidere con el número de terceros y con `report` como default | acá §4.3 |
| 9 | `scan-diff` post-fan-out + cerrar el bypass por Bash | plan §5.1 |
| 10 | cadena graphify→engram en `/batten-build` | plan §5.3 |
| 11 | decidir campo por campo: implementar o sacar | plan §6 |

Los ítems 5 a 8 son los de adopción y **ninguno toca el motor**. Se pueden hacer en paralelo con
las correcciones técnicas, por alguien distinto, sin riesgo de conflicto.

---

## Fuentes

- [superpowers](https://github.com/obra/superpowers) — ~224.700 ★, framework de metodología obligatoria
- [graphify](https://github.com/Graphify-Labs/graphify) — ~97.500 ★, `EXTRACTED`/`INFERRED`, `GRAPHIFY_HOOK_STRICT` opt-in
- [caveman](https://github.com/juliusbrussee/caveman) — ~54.000 ★, −65 % tokens medido
- [Reason Less, Verify More](https://arxiv.org/html/2607.07405v1) — arXiv:2607.07405, 8 jul 2026
- [CoAgent](https://arxiv.org/abs/2606.15376) — arXiv:2606.15376, 13 jun 2026
- [S-Bus](https://arxiv.org/pdf/2605.17076) — arXiv:2605.17076, may 2026
- [Claude Code Plugins: directorio oficial](https://claudecamp.ai/blog/claude-code-plugins-official-directory)
- [8 Spend Management Tools for Claude Code](https://www.toriihq.com/articles/spend-management-tools-claude-code) — el paisaje de observabilidad de costo
