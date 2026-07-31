# Prueba de campo

> [English](FIELD-TEST.md) · **Español**

> batten corrido contra una réplica de un proyecto real y un repo construido desde cero, por
> agentes que nunca lo habían visto. 90 comportamientos confirmados funcionando, 80 hallazgos,
> cada uno de ellos verificado o refutado por un segundo agente cuyo trabajo era demostrar que
> estaba mal. 2026-07-28.

## Por qué existe esto

Todo lo que batten afirma es que una regla deja de ser un pedido y pasa a ser un mecanismo. Un
documento puede pedirle a un agente que no apruebe su propio trabajo; un hook `PreToolUse` puede
rechazar el commit. Esa afirmación solo vale algo si el mecanismo de verdad aguanta, y la única
forma de averiguarlo es ponerlo frente a alguien que no sepa de antemano dónde están las
costuras.

Entonces: siete dimensiones, ejercitadas por agentes que recibieron los docs y nada más. Después
una segunda pasada en la que cada hallazgo se le entregó a un agente fresco cuyas instrucciones
eran **refutarlo**, con veredicto por default *refutado* salvo que reprodujeran el defecto ellos
mismos sobre un binario compilado desde `HEAD`.

El número de titular no son los 80 hallazgos. Es que la corrida atrapó una regresión introducida
**cuatro commits antes, en la misma sesión** — un cambio que yo había escrito, revisado y creído.

## Qué se probó

Una réplica aislada (`replica-ui`) del repo privado contra el que batten fue diseñado: cuatro
dominios cada uno con su propio `AGENTS.md`, un
`AGENTS.md` y un `CLAUDE.md` de raíz, 47 skills, 9 agentes custom, un backlog de
`US-001`..`US-0NN`, y todavía nada de código. Más un repo de demo construido desde cero
específicamente para recorrer el camino de adopción de cinco pasos.

| dimensión | qué ejercitó |
|---|---|
| `init` | escanear un repo que nunca vio y proponer un spec |
| `gate` | el verdict gate sobre `git commit` |
| `writeset` | la cerca del fan-out entre subagentes paralelos |
| `lifecycle` | fases, el ancla, el close, multi-unit |
| `observability` | el grafo de la corrida, el canvas, la TUI |
| `budget` | el ledger de tokens/dólares/cuota y sus techos |
| `demo-zero` | adopción desde un directorio vacío |

No se tocó nada fuera de un sandbox, y eso se verificó después en vez de asumirse. El repo que
replica sigue teniendo un solo commit, un árbol limpio y ningún `batten.yaml`. La
`~/.batten/batten.db` real contiene solo las corridas de dogfood de este mismo repo.

## Resultados

| | |
|---|---|
| comportamientos confirmados funcionando | 90 |
| hallazgos | 80 |
| verificados adversarialmente | 80 (17 durante la corrida, 63 en la segunda pasada) |
| **confirmados** | **52** |
| refutados | 11 |

Hallazgos confirmados, después de que el verificador re-juzgara la severidad con independencia
del reportero:

| severidad | cantidad |
|---|---|
| blocker | 3 |
| major | 26 |
| minor | 17 |
| polish | 6 |

Once refutaciones, que importan tanto como las confirmaciones:

- **6 eran duplicados.** Tres reformulaciones de las mismas entradas faltantes en `--help`, dos
  de los mismos nodos de fase sin terminar, una del mismo cero fabricado. Dimensiones distintas
  encontraron el mismo defecto por superficies distintas, lo cual es buena señal sobre la
  cobertura y mala señal sobre contar hallazgos.
- **2 eran diseño correcto confundido con un bug.** El canvas no tiene aristas de fase a fase
  porque el esquema no tiene esa relación — el orden de fases es posicional y está documentado
  como tal. Y un write-set *sí* se libera, con `batten close --status failed`, cosa que el
  reporte afirmaba que no existía.
- **1 ya estaba arreglado** en `HEAD` por un commit que aterrizó después de la corrida.
- **2 eran reales pero más chicos que como se reportaron.** `batten show` sí nombra el check que
  falla, al contrario de lo que decía el reporte. Y el ancla de 7 caracteres no puede ser
  ambigua: `git rev-parse --short` es un *mínimo*, y el verificador lo demostró construyendo un
  repo con una colisión real de 4 caracteres y viendo a git devolver cinco.

## Las dos trampas

Verificar hallazgos sobre una herramienta de gobernanza tiene dos modos de falla que producen
respuestas seguras y equivocadas. Los dos se le explicaron a cada verificador.

**Comportamiento deliberado que parece un bug.** batten falla abierto con una advertencia cuando
no puede atribuir una escritura, da un aviso en vez de una denegación cuando no puede asignar
culpa, y reporta una cantidad no medible como no medible en vez de como cero. Cada una de esas es
una decisión, no un descuido — rechazar todos los commits en un repo que todavía no declaró sus
checks solo consigue que batten se desinstale el primer día. Reportarlas como defectos habría
producido «fixes» que empeoran la herramienta. Ocho hallazgos entre las dos pasadas fueron
refutados exactamente por esto.

El inverso es un defecto real, y la distinción es nítida: reportar una cantidad *no medida* como
un `0` duro es la falla que todo el diseño existe para prevenir. Dos hallazgos confirmados eran
exactamente eso.

**El silencio no es evidencia de allow.** `batten hook` no imprime nada y sale con 0 por al menos
seis razones distintas: allow, no encontró spec, una falla del store, stdin malformado, un panic
recuperado, un evento desconocido. Toda afirmación de que algo se permitió exigía entonces un
control positivo apareado — el mismo payload con un campo cambiado de forma que la denegación sea
obligatoria. Si el control también quedaba en silencio, el hook nunca se enganchó y el «PASS» no
probaba nada.

Esto no es rigor hipotético. El blocker de abajo se probó exactamente así: payload idéntico byte
a byte, una línea de `checks:` devuelta a su lugar, y aparece la denegación.

## Los tres blockers

Los tres se confirmaron por reproducción, no leyendo código.

### 1. `on_exceed: block` dejó de imponerse — una regresión de cuatro commits antes

El commit `24d8e4c` agregó un aviso para un gate que no declara `checks:`. Ese aviso es correcto
y sigue en pie. Lo que estaba mal es que la rama **retornaba**, así que todo lo que venía después
se salteaba — incluida la evaluación de presupuesto.

El resultado: un repo que todavía no había declarado sus checks también perdía en silencio su
techo de presupuesto. Dos condiciones que no tienen nada que ver entre sí, acopladas por flujo de
control.

El A/B, sobre una corrida 7.8× pasada de su techo de tokens bajo `on_exceed: block` y
`enforcement: enforce`:

```
gates.qa.checks: []        -> advisory only, commit lands
gates.qa.checks: ['...']   -> permissionDecision: deny, citing the budget
```

Mismo binario, misma base de datos, payload idéntico byte a byte. Este es el hallazgo que
justifica por sí solo haber corrido la prueba de campo: esa regresión la escribí yo, la revisé, y
la despaché cuatro commits antes de que un agente la encontrara.

### 2. Un id de nodo que no lleva su corrida no es un identificador

Los nodos de fase se nombraban `"p-" + phaseID`, y `node_id` es una `PRIMARY KEY` bajo un
`INSERT OR REPLACE`. Así que una fase llamada `build` era **una sola fila para toda la base de
datos**. El segundo unit en entrar a `build` reescribía la fila del primero para apuntar a su
propia corrida.

Reportado de forma independiente por dos dimensiones, y probado en la capa de almacenamiento y no
en el renderer: el verificador leyó la fila de vuelta desde SQLite y vio cambiar su `run_id`. El
canvas del primer unit colapsó de cuatro nodos a un encabezado pelado; su árbol en la TUI decía
"no nodes recorded yet". Los ids de subagente colisionaban de la misma manera, porque los ids de
agente solo son únicos dentro de la sesión que los acuñó.

Dos work items abiertos a la vez es el caso de uso insignia.

### 3. Los renderers tiraban lo que la colisión dejaba atrás

Scopear los ids era necesario y no suficiente. Tanto el canvas como la TUI agrupaban a un hijo
bajo lo que fuera que nombrara su arista de spawn, y solo rescataban huérfanos cuyo padre era la
*cadena vacía*. Un subagente cuyo id de padre no resolvía desaparecía de las dos superficies —
mientras `batten show`, leyendo la misma base de datos, todavía lo listaba.

Un agente que falta es exactamente el que alguien abre el canvas para encontrar.

## Dos más que la pasada de verificación encontró sola

Ninguno estaba en el reporte original. Los dos tienen la misma forma que la promesa central del
gate, y los dos son la razón de que a los verificadores se les dijera reproducir en vez de
revisar.

**`batten check` solo podía cerrar un unit.** Escribe su propio veredicto `source='batten'`, y
esa fila era a la vez el veredicto más nuevo *y* el batten-verified — así que satisfacía las dos
condiciones del gate por sí sola. `batten check` sobre un diff vacío, todavía en la fase de
build, sin que nada hubiera juzgado los criterios de aceptación, dejaba el camino libre para un
commit. El gate era unilateral: el veredicto de un agente por sí solo denegaba; el propio de
batten, pasaba.

**El mensaje de commit nunca se leía.** Una sesión atada a un `TASK-1` verificado podía aterrizar
`feat(TASK-2): ...` mientras `TASK-2` no tenía veredicto alguno — la revisión de un unit
acreditada al trabajo de otro. Bajo desarrollo trunk-based, donde la rama no nombra nada, el
mensaje es la única señal que hay. `batten.schema.json` había prometido esta resolución todo el
tiempo.

## Qué se arregló

Todos los blockers, los dos agujeros nuevos del gate, y los hallazgos confirmados que compartían
su causa raíz. Cada fix lleva un test que falla contra el commit anterior a él.

| fix | cierra |
|---|---|
| el aviso se retiene y se devuelve al final, así una denegación de presupuesto todavía puede pasarlo | 30 |
| `commitRe` matchea `git -c k=v commit`, `git -C dir commit`, `git.exe commit` | el blocker original #1 |
| los ids de nodo se construyen con `store.PhaseNodeID` / `store.AgentNodeID`, así los dos productores no pueden divergir | 5, 14, 38 |
| un hijo cuyo id de padre no resuelve aterriza en la columna de no atribuidos | 39 |
| cerrar una corrida termina sus nodos de fase | 18, 44, 55 |
| la denegación de write-set imprime el id con el que se lanzó el agente, y dice qué hacer cuando no es dueño de nada | 8, 61 |
| una corrida no medida se reporta como no medida, nunca como `0 tokens, $0.00` | 40, 57 |
| `batten ingest` reporta lo que la cerca de tiempo dejó afuera, en tokens y en dólares | 29 |
| los techos no disponibles llevan la razón real en vez de "install the statusline" | 35 |
| un commit sin gate o inatribuible lo dice en vez de pasar en silencio | 20, 49 |
| el gate exige dos veredictos de dos productores distintos, y `close` usa la misma regla | 9, 15 |
| el mensaje de commit decide qué unit se gatea | 22, 50 |

Los hallazgos confirmados restantes — la mayoría de los 52 — quedaron registrados en
`field-test/verified.json` con su reproducción, evidencia verbatim, el control positivo que se
corrió, y un `fix_hint` nombrando archivo y línea. **Ese directorio se retiró del árbol** antes
del primer tag: describía un repositorio privado y no la réplica sintética, y un documento sobre
el codebase de otro no es publicable por más reescritura que se le haga. Está en la historia de
git. Su estado actual — 45 arreglados, 7 abiertos, cada abierto con nombre — está en
[CHANGELOG.es.md](../CHANGELOG.es.md) bajo *Known gaps*.

## Qué dice esto del método

Tres cosas que vale la pena conservar.

**Reproducir, no revisar.** Los verificadores que leían código producían hallazgos plausibles;
los que corrían comandos producían hallazgos correctos. Cada veredicto en `verified.json` que
dice CONFIRMED tiene atrás una lista de comandos y salida pegada, y los que no pudieron ejecutar
el camino quedaron marcados PLAUSIBLE en vez de promovidos.

**Default a refutado.** Once de 63 hallazgos no sobrevivieron, y seis de esos eran duplicados que
un proceso orientado a contar habría despachado como seis bugs separados. Las refutaciones
también atraparon dos casos en los que «arreglar» el hallazgo habría empeorado la herramienta.

**Batchear el trabajo.** El primer intento de esta verificación murió por un límite de sesión
tratando de hacer los 63 de una, perdiendo 55 agentes y la síntesis. De a cinco, en secuencia,
terminó el mismo trabajo sin perder nada. El tamaño de batch es la única razón de que este
documento exista.

## Reproducirlo

**Lo que podés correr, y es la parte que importa.** La réplica contra la que se ejecutó esto es
un script commiteado, y también lo es la matriz de aceptación sobre ella:

```bash
scripts/replica-ui.sh <sandbox>      # rebuilds the fixture from scratch
scripts/matrix-replica.sh <sandbox>  # 41 assertions over it
scripts/matrix-demo.sh <sandbox>     # 26 over the from-zero adoption path
```

Eso es deliberado, y salió de este ejercicio: la matriz solía ser prosa en un documento con ocho
tests numerados, mientras que las cuentas que se reportaban (11/11, después 12/12) no coincidían
con ninguna lista escrita — los tests más nuevos vivían en la memoria de quien los hubiera
corrido. Una matriz de aceptación que nadie más puede re-correr exactamente no es una matriz, es
un recuerdo.

**Lo que no podés, y por qué se dice en vez de omitirse en silencio.** El material crudo — 63
veredictos con reproducciones por hallazgo, evidencia verbatim y controles positivos, más los 80
hallazgos tal como se reportaron y los retornos por dimensión — estaba en `docs/field-test/`. Se
**retiró del árbol antes del primer tag**: describía el repositorio privado sobre el que se
modeló la réplica, no la réplica, y un documento sobre el codebase de otro no es publicable por
más reescritura que se le haga. Está en la historia de git, que es donde corresponde que viva una
decisión de dejar de publicar algo, y no en una purga que invalidaría cada SHA de commit que este
proyecto cita.

Así que la declaración honesta de lo que sobrevive: el análisis es este documento, el estado de
los hallazgos está en [CHANGELOG.es.md](../CHANGELOG.es.md) bajo *Known gaps* (45 arreglados, 7
abiertos, cada abierto con nombre), y la evidencia ejecutable son los tres scripts de arriba. Los
pasos de repro por hallazgo no son públicos.

**Si corrés cualquiera de esto vos mismo:** exportá `BATTEN_DB` hacia tu sandbox antes de cada
comando de batten. `dbPath()` cae de vuelta a la base de datos real en el momento en que queda
sin setear, y una prueba de campo que contamina el vault propio del usuario falló antes de
empezar.
