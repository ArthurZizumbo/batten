# batten — qué es, qué funciona, qué sigue

> [English](ROADMAP.md) · **Español**

> El documento vivo. [README.es.md](README.es.md) ([en](README.md)) es el pitch;
> [DESIGN.es.md](DESIGN.es.md) ([en](DESIGN.md)) es el razonamiento detrás de la forma. **Este
> archivo es el inventario honesto**: qué está probado, qué está apenas construido, qué falta, y qué
> no decidimos todavía.
>
> Regla para este archivo: **nada se lista como hecho sin evidencia.** "Construido" y "verificado"
> son columnas separadas porque son hechos separados, y mezclarlos es la forma en que un proyecto
> empieza a mentirse a sí mismo.
>
> Última actualización: **2026-07-27**.

---

## La versión en un párrafo

Un agente de código tiene tres memorias. **Estructural** — qué *es* el código — le pertenece a
[graphify](https://github.com/Graphify-Labs/graphify). **Episódica** — qué *decidimos*, y por qué —
le pertenece a [engram](https://github.com/Gentleman-Programming/engram). La tercera es
**procedural**: *cómo trabajamos.* Nadie la tenía. Vive en prosa — un `CONTRIBUTING.md`, un archivo
de prompt de 700 líneas — y la prosa no puede detener un `git commit`. batten convierte esa prosa en
un `batten.yaml` que un binario en Go **impone** con hooks de Claude Code. Las reglas dejan de ser
consejo y pasan a ser denegaciones.

La restricción que gobierna, la que decide toda discusión de alcance:

> **Si un hook no puede imponerlo o un comando no puede correrlo, no va en `batten.yaml`.**

---

## Verificado — probado con evidencia, no afirmado

| Capacidad | La evidencia |
|---|---|
| **Verdict gate** | `git commit` sin veredicto → `DENY`. Con `result: blocked` → `DENY`. Con `ok` + evidencia → `ALLOW`. Un sobre con `evidence[]` vacío es rechazado por el binario antes de llegar a la base de datos |
| **Guard de write-sets** | El agente B editando el archivo del agente A → `DENY`, nombrando al dueño y el write-set propio de B. Impuesto por una `PRIMARY KEY (run_id, path)`, así que que los write-sets sean disjuntos es una restricción de base de datos, no un consejo |
| **Contabilidad de tokens de subagentes** | 21k tokens contados = 6k del padre (duplicado descartado) + 15k del subagente (archivo de transcript separado). Un parser ingenuo pierde el 71 %. Verificado contra un transcript real de 1.9 MB |
| **Presupuesto honesto** | Tres techos (tokens / USD imputados / % de la cuota móvil). Un techo que no se puede medir reporta `NOT MEASURABLE`, nunca un cero fabricado |
| **Multi-sesión** | Vínculo sesión↔corrida; write-sets defendidos *entre* corridas abiertas; la ambigüedad se muestra en vez de adivinarse. Verificado con US-034/sessA vs US-051/sessB |
| **Neutralidad** *(criterio bloqueante para v1)* | El mismo binario corrió sobre un repo de ML (7 dominios, H100, MLflow) y una webapp en TS (`TCK`, frontend/api). Solo cambió `batten.yaml` |
| **Hooks en Windows** | La forma exec `${CLAUDE_PLUGIN_ROOT}/bin/batten` **sin** `.exe` resuelve en Windows 11. Era un riesgo de diseño abierto; costó cero cambios |
| **Latencia de hooks** | 17 ms de mediana en el fast-path, contra un presupuesto de <50 ms |
| **Dogfood real** | El plugin instalado y gobernando este repo. Spike E0 5/5. **7 bugs encontrados usándolo** (`2674521`, `a0fb8f1`, `b2c465c`, `face412`, `3c33dc5`, `28fe87b`, `e02670f`) |
| **Construido por su propio flujo** | TASK-2 se planificó, se abrió en fan-out a 2 subagentes con write-sets disjuntos, se verificó y se cerró por el propio gate de batten — `abea1ff` |
| **Grafo de código vivo** | graphify 0.9.25 conectado: 1043 nodos / 2100 aristas / 65 comunidades. Los god nodes del reporte suben el tier de dificultad de la fase de plan — tocar un god node nunca es "mecánico" |

## Cobertura de tests, honestamente

Todos los paquetes tienen tests salvo `internal/tui`, un visor Bubbletea de solo lectura. Total
**52.7 %**, y la distribución importa más que el número:

| paquete | cobertura | por qué este nivel |
|---|---|---|
| `internal/spec` | 94.9% | el parser del que dependen todos los demás paquetes, y la pieza a la que un repo desconocido le da de comer YAML desconocido |
| `internal/usage` | 94.2% | contabilidad de tokens de subagentes — un parser ingenuo pierde el 71 % |
| `internal/vault` | 92.1% | |
| `internal/canvas` | 86% | el lector del formato es Obsidian, que falla en silencio |
| `internal/mcp` | 86% | |
| `internal/export` | 84.8% | |
| `internal/store` | 62.5% | las garantías están cubiertas; las queries de reporting no |
| `internal/hooks` | 29.5% | las dos denegaciones y la asimetría del guard están cubiertas; la plomería de eventos no |
| `cmd/batten` | 8% | mayormente parseo de flags y salida de consola; las decisiones que contiene están cubiertas |
| `internal/tui` | 0% | un visor de terminal que lee el store y lo renderiza |

Escribir estos tests encontró cuatro bugs reales, que es el argumento a su favor: una colisión de
run-id en Windows, una "última corrida" no determinística, un gate que pasaba en silencio cuando no
podía verificar nada, y un hint de `doctor` apuntando a un comando que ya no existe.

## Construido, todavía no probado

Estado honesto: el código existe y sus tests unitarios pasan, pero ninguna corrida fuera de este
repo lo ejercitó.

- **Instalarlo en un repo que ya está a mitad de desarrollo.** El punto entero, y solo pasó acá.
  Ver *La prueba de fuego* más abajo.
- **Migración con `--from` desde un doc de flujo en prosa.** Implementada; nunca corrió sobre el
  doc de otra persona.
- **TUI interactiva.** Compila, tiene tests, nunca la abrió una segunda persona en una terminal
  real.
- **Camino de release con GoReleaser.** El workflow existe; nunca se pusheó un tag.

## No construido

- **Integración con headroom.** `capabilities.compression.measure` ya cuenta tokens por nodo, así
  que la comparación es *medible* — pero headroom en sí no está instalado y la comparación nunca
  corrió.
- **`graphify hook install`** para reconstruir el grafo automáticamente en cada commit.
- **Aristas doc→código en el grafo.** La capa semántica produjo 84 nodos de documentación que
  enlazan a `.yaml` y entre sí, pero **cero aristas `.md`↔`.go`**. La capa de conocimiento y la
  capa AST conviven una al lado de la otra sin tocarse. Es un límite del lado de graphify, no de
  batten.

---

## La prueba de fuego

Todo lo de arriba es prólogo hasta que esto funcione, en un repo que no sea este:

```
1. /plugin marketplace add <repo>       # once per machine
2. /plugin install batten@batten        # the binary arrives on its own
3. /batten-init                          # interviews the repo; proposes batten.yaml
4. batten statusline --install           # optional: enables the quota ceiling
5. batten doctor                         # green → working
```

— y tiene que entrar **sin romper el sprint**. Los gates arrancan en `enforcement: report` (avisan,
no bloquean) y se endurecen a `enforce` cuando el equipo confía en ellos.

**Próximo objetivo: el repo privado contra el que esto se diseñó** — el fixture que
`scripts/replica-ui.sh` reconstruye. Es el caso ideal *y* el caso difícil, porque **ya tiene un
harness** — uno considerablemente más rico de lo esperado: un `AGENTS.md` en la raíz y uno por
dominio (`backend/`, `db/`, `frontend/`, `ml/`), un `CLAUDE.md`, **47 skills y 9 agentes custom**
bajo `.claude/`, y un backlog formal de `US-001`..`US-0NN` en `context/planeacion_proyecto.md`.
batten tiene que leer lo que hay y complementarlo — nunca pisar un proceso que ya funciona.

Dos cosas suyas le dan forma a la prueba en vez de ser incidentales a ella. Todavía **no tiene
código**: cada directorio de dominio contiene solo su `AGENTS.md`, así que no hay archivos de build
y por lo tanto no hay checks, lo que significa que el gate arranca sin verificar nada y tiene que
decirlo. Y tiene **una sola rama**, así que no hay historial de nombres de rama del que aprender
una convención de unit — la convención está en el backlog.

---

## Próximos pasos, en orden

1. ~~**`batten-init` tiene que complementar, no imponer.**~~ **Hecho.** El scan reporta el harness
   ya presente, el stack a partir de archivos marcadores, y dónde se describe el repo a sí mismo;
   el comando lee eso primero y después entrevista al humano en vez de adivinar en silencio.
   Correrlo contra ese repo — planificado por completo pero todavía no construido — fue lo que
   encontró dos bugs: una lista de dominios que volvía vacía porque exigía código, y una nota que
   afirmaba que los checks venían de archivos de build que no existían.
2. **Probar la prueba de fuego en ese repo.** El scan ahora lo lee correctamente, y llegar ahí
   tomó cuatro arreglos que él mismo sacó a la superficie: una lista de dominios que volvía vacía
   porque exigía código, una nota que afirmaba que los checks venían de archivos de build que no
   existían, una convención de unit leída de nombres de rama cuando estaba escrita en el backlog, y
   sugerencias de skills matcheadas contra prosa en vez de contra nombres. Ahora propone
   `US`/`US-\d{3}` con el documento de plan y el locator correctos, cuatro dominios desde sus
   `AGENTS.md`, skills por dominio defendibles, y un honesto "NO check command was found — the gate
   currently verifies nothing".

   Lo que queda es la parte que ningún scan puede hacer por vos: llenar los invariants desde esos
   cinco archivos `AGENTS.md`, y correr un work item real de punta a punta. Ver `docs/FIELD-TEST.es.md`
   para lo que una corrida sandboxeada de toda la superficie encontró de verdad.
3. **Primer release público.** El placeholder ya no está: el module path, los dos scripts de
   bootstrap, el manifiesto del plugin, la entrada del marketplace, el `$id` del schema y los docs
   de instalación apuntan todos a `ArthurZizumbo/batten`. Lo que queda es crear el repo, pushear, y
   taggear `v0.1.0` — GoReleaser construye todas las plataformas desde ahí, y `release.yml` ahora
   se niega a arrancar hasta que la suite pase en Linux, macOS y Windows.
4. **headroom**, cuando haya un segundo repo real sobre el cual medirlo. Medir compresión en un
   solo repo te habla de ese repo.

---

## Preguntas abiertas — la parte para discutir

Esta sección existe para que las decisiones de nombres y de alcance sean visibles en vez de
acumularse por default. Ninguna está cerrada.

### Nombres

| Hoy | La tensión | Opciones |
|---|---|---|
| **El directorio local es `LoopWorkFlow`, el producto es `batten`** | ~~Decidido:~~ el module path, el plugin, la entrada del marketplace y todos los docs dicen ahora `ArthurZizumbo/batten`, así que el repo de GitHub tiene que llamarse `batten`. La carpeta local puede quedarse con su título de trabajo; nada la lee | — |
| `unit` | El spec dice `unit`, la CLI dice `--unit`, los docs dicen "work item", y los usuarios dicen "ticket" o "US" | mantener `unit` como el término del schema y dejar de decir "work item" en prosa; o renombrarlo a `item` |
| `verdict envelope` | Preciso y nuestro, pero nadie llega sabiéndolo de antemano | mantenerlo — la precisión es el punto, y `gate result` invita a la vaguedad que el gate existe para matar |
| `capabilities.compression` | Nombrado por el mecanismo (compresión), no por el objetivo (headroom) | ¿renombrar a `capabilities.headroom`? pero entonces lleva el nombre de un vendor |
| `enforcement: report \| enforce` | ¿Hay un tercer modo — enforce para algunos gates, report para otros? | un `enforcement:` por gate que pise el global |
| `batten claim` | El comando reclama un write-set; el sustantivo `claim` no aparece nunca en el spec | alinear en una sola palabra — `writeset` en los dos, o `claim` en los dos |

### Alcance

- **¿batten es dueño del artefacto de resolución, o solo exige uno?** Hoy `/batten-close` lo
  escribe. Es la cosa más opinionada de todo el producto, y la menos imponible.
- **¿`batten.yaml` debería soportar presupuestos por dominio?** Un dominio de ML y un dominio de
  docs no merecen el mismo techo. En contra: cada perilla que se agrega al spec es una perilla que
  alguien tiene que mantener.
- **¿El spec del dogfood debería siquiera vivir en el repo?** El `batten.yaml` en la raíz declara
  un vault bajo `~`, que es lo bastante portable para publicarse, pero sigue siendo el setup de una
  persona presentado como el del proyecto. Es un ejemplo real, lo cual es útil, y es la máquina de
  alguien, lo cual no.

---

## Lo que batten deliberadamente no hace

Las partes concurridas de este espacio están concurridas de buenas herramientas.

- **No guarda memoria episódica.** Ese es el trabajo de engram.
- **No construye un grafo de código.** Ese es el trabajo de graphify — tree-sitter, determinístico,
  cero tokens de LLM para la capa estructural. batten lo consulta cuando está y grepea cuando no.
- **No re-orquesta al agente.** Los workflows dinámicos de Claude Code ya corren bien el fan-out.
  batten lo gobierna — los rieles, no el motor.
- **No comprime contexto.** Si headroom ayuda a *tu* fan-out, usalo; batten cuenta tokens por nodo
  para que puedas averiguarlo en vez de confiar en un README.
- **No puede cambiar el modelo del loop principal.** Eso es el `/model` del usuario, y ningún
  plugin llega ahí. El ruteo de modelos aplica entonces **solo a subagentes** — la herramienta
  Agent toma un parámetro `model` y los agentes custom llevan uno en su frontmatter. Lo que batten
  agrega es la mitad que nadie más tiene: el ledger registra qué modelo usó *de verdad* cada nodo,
  así que `batten show` puede marcar dónde el ruteo declarado y el real divergieron. Declarar un
  tier para el loop principal sería un deseo; verificar el del fan-out es un hecho.

## Principios que no se doblan

1. **Nunca inventar un número.** Lo que no se puede medir se reporta como no medible.
2. **Degradar, nunca romper.** Un hook sin spec, sin binario, o con input malformado hace no-op en
   silencio. Nunca tira abajo una sesión.
3. **Fallar abierto solo en voz alta.** Si el gate no puede atribuir el trabajo, no bloquea — y lo
   dice.
4. **Un override va al log.** Siempre.
