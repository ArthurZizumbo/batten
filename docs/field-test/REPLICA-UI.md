# Validación contra la réplica de proyecto_ui

> Ejecutada el **2026-07-28** sobre `e52d931`, con el binario compilado de ese commit.
> Réplica generada por [`scripts/replica-ui.sh`](../../scripts/replica-ui.sh).
> `BATTEN_DB` exportado dentro del sandbox antes de **cada** comando; ni la DB real
> (`~/.batten/batten.db`) ni `proyecto_ui` se tocaron.

Los 8 fixes del field test se validaron sobre sandboxes sintéticos y sobre `taskly`, que es un
repo git con código Go, tests y un `Makefile`. **proyecto_ui no es ninguna de esas cosas**, y la
diferencia que importa es la última fila:

| | `taskly` | réplica de proyecto_ui |
|---|---|---|
| repo git | sí | **no lo es** |
| código | Go con tests | **ninguno**: 4 dominios con solo su `AGENTS.md` |
| backlog | 5 items | 43 `US-0NN` |
| build files | `Makefile` | ninguno → **`gates.checks` vacío** |

Un repo sin build files cae en la rama del gate **sin checks**, que es exactamente donde vivía la
regresión de `on_exceed: block` (`53227e8`). Esa rama nunca se había ejercitado con esta forma.

---

## Re-corrida del bloque 3 — 2026-07-29

> **Las dos matrices pasan enteras: 35/35 en la réplica de proyecto_ui, 26/26 en `batten demo`.**
> La réplica se regeneró desde cero antes de medir, y el binario se compiló del árbol del momento.

**Y ahora las dos matrices SON SCRIPTS**, no una lista en un documento:

```bash
scripts/matrix-replica.sh   # regenera la réplica, compila, y corre las 35
scripts/matrix-demo.sh      # compila y corre las 26 sobre `batten demo`
```

Esto se hizo porque la matriz tenía el problema que el resto del proyecto existe para eliminar. El
documento enumeraba **ocho** pruebas; los conteos que se reportaban al cerrar cada bloque eran
11/11 y después 12/12, y las pruebas que faltaban **no estaban escritas en ninguna parte** — vivían
en la memoria de quien las había corrido. Una matriz de aceptación que nadie más puede re-correr
exactamente no es una matriz, es un recuerdo.

Los conteos suben de 12 y 16 a 35 y 26 porque ahora cada aserción cuenta por separado (antes se
agrupaban por escenario) y porque el bloque 3 agregó escenarios nuevos. No es que hayan aparecido
23 pruebas de la nada: es que ahora están todas escritas.

### Lo que la re-corrida encontró

**Un defecto real, y es exactamente para esto que existe la matriz.** `batten init` escribía un
bloque `budget:` **sin `max_iterations`**. Mientras el techo de iteraciones era prosa eso no costaba
nada; ahora que es el mecanismo que la regla 2 impone, un spec generado sin él es un spec generado
que **desactiva un guard por omisión**: un repo que acaba de adoptar batten corría `/batten-night`
sin techo, y `batten iterate` contestaba —correcta e inútilmente— *"no budget.max_iterations
declared, so nothing stops this"*.

Arreglado en `internal/scan/scan.go`, con un test que falla contra el commit anterior.

**Y dos aserciones mías que estaban mal, anotadas porque el error es instructivo:**

- Una comprobaba que la base real del usuario no se hubiera tocado **por mtime**. Falló, y no por
  contaminación: el plugin instalado corre sobre el repo del usuario en cada llamada de herramienta,
  así que ese mtime cambia todo el tiempo por operación legítima. La comprobación correcta es que
  **ningún proyecto de sandbox aparezca en esa base** — verificado, y no aparece ninguno.
- Otra buscaba la cadena `domain(s)` en la salida del demo, que dice `2 domains detected`. Una
  matriz cuyas aserciones no coinciden con la salida real reporta rojos que no existen, y eso
  entrena a la gente a ignorarla.

### Las 35 de la réplica

| # | escenario | qué prueba (aserciones) |
|---|---|---|
| 1 | `init` sobre 4 dominios sin código y 43 items | el unit sale del **backlog**, no de las ramas · reconoce el proceso que el repo ya tiene (3) |
| 2 | `doctor` en UNA pasada | avisa que el gate no verifica nada · el ancla que el repo no puede producir · dice que los gates sólo avisan · **cruza `query_before_read` con la realidad** · 14 líneas en una corrida (5) |
| 3 | gate **sin checks** + presupuesto excedido → DENY | denegado · con código tipado · **control positivo**: un no-commit sale en silencio Y el log registra allow *y* deny, así que el hook corrió (5) |
| 4 | dos units en la misma fase | ninguno se roba las filas del otro (2) |
| 5 | commit cuyo mensaje nombra otro unit | nombra el unit del mensaje · no pasa en silencio (2) |
| 6 | `diff_from: anchor` **sin ancla** | lo dice · nombra la fase que debía grabarla (2) |
| 7 | `scan-diff` sin ancla | sale ≠0 · distingue **no-medible** de limpio (2) |
| 8 | `worktree` sobre un repo que no es git | se niega · dice exactamente qué falta (2) |
| 9 | **las cuatro reglas desatendidas** | rm denegado · override rechazado · techo impuesto · commit denegado · **control**: con el modo apagado el rm pasa (5) |
| 10 | `sed -i` sobre el archivo de otro agente | nombra el archivo · nombra cómo se iba a escribir · es **aviso**, no denegación (3) |
| 11 | la cadena de memorias en `SessionStart` | la instrucción se inyecta · exige declarar si ninguna respondió (2) |
| 12 | degradación total sin git, sin checks, sin build files | la base quedó dentro del sandbox · la base real no tiene ningún proyecto de sandbox (2) |

### Las 26 del demo

| # | escenario | aserciones |
|---|---|---|
| 0 | el demo corre entero | sale 0 · borra su sandbox · dice que no tocó nada del usuario (3) |
| 1 | `init` lee el repo | el paso existe · detecta los dominios de sus `AGENTS.md` · deriva el unit del backlog · los checks salen de los build files (4) |
| 2 | commit antes de abrir un run | avisa que **no está gobernando** (1) |
| 3 | `phase` abre el run y ancla | (1) |
| 4 | commit sin veredicto | DENY (1) |
| 5 | el revisor aprueba y **sigue** denegado | un veredicto de agente no alcanza (1) |
| 6 | colisión de write-set entre dos agentes | se deniega · y **no ofrece salida**: el plan está mal (2) |
| 7 | `check` corre de verdad y uno falla | el paso existe · el fallo se reporta como fallo (2) |
| 8 | arreglado el bug, los checks pasan | (1) |
| 9 | ahora el commit se permite | (1) |
| 10 | algo edita el árbol después del check | `stale_target` detectado · dice cómo re-verificar (2) |
| 11 | `report` | el bloque de impacto · cuenta los denegados · **dice desde cuándo cuenta** · el uso sin medir no es 0 (4) |
| 12 | el sobre tipado | `batten.code` · `batten.retry` · `batten.fix` (3) |

---

## Historial de re-corridas

| fecha | commit | réplica | demo | qué encontró |
|---|---|---|---|---|
| 2026-07-28 | `e52d931` | 6/8 | — | el silencio del primer commit, el ancla muda, el verde falso de graphify |
| 2026-07-28 | `04b2065` | 11/11 | 15/15 | nada nuevo (los tres arreglados) |
| 2026-07-29 | bloque 2 | 12/12 | 16/16 | nada nuevo — **pero la lista de pruebas no estaba escrita** |
| 2026-07-29 | bloque 3 | **35/35** | **26/26** | `init` no escribía `max_iterations`, y dos aserciones propias mal escritas |

---

## Re-corrida anterior — 2026-07-29, sobre `04b2065`

> Las dos matrices pasaban enteras: 11/11 en la réplica, 15/15 en `batten demo`.
> Los dos hallazgos de abajo están arreglados (el silencio del primer commit en `5b02b0c`, el
> ancla muda y el verde falso de graphify en `d850c23`).

Y ahora hay una **segunda** matriz. `batten demo` construye su propio repo, corre el flujo entero
y lo borra — así que es a la vez el recorrido de adopción y el test de integración end-to-end que
el proyecto no tenía. Sus 15 comprobaciones cubren lo que la réplica no puede: las dos cuñas
enfrentadas de verdad (un check que **corre** y falla por un motivo real, una colisión de
write-set entre dos agentes), el target estancado, y las tres promesas de aislamiento.

| | réplica de proyecto_ui | `batten demo` |
|---|---|---|
| pruebas | 11 | 15 |
| repo git | **no** | sí |
| código | ninguno | dos dominios con un bug real |
| lo que sólo ella prueba | la degradación sin git, sin checks y sin build files | el check que corre, la colisión, el target estancado, el aislamiento |

---

## Resultado de la primera corrida: 6 de 8 pasan, 1 falla, 1 pasa a medias

| # | prueba | resultado |
|---|---|---|
| 1 | `init` sobre 4 dominios sin código y 43 items | ✅ |
| 2 | gate **sin checks** + presupuesto excedido → DENY | ✅ con control positivo |
| 3 | dos units en la misma fase → los dos canvas intactos | ✅ |
| 4 | `git -c user.name=… commit` entra al gate | ✅ con control positivo |
| 5 | primer commit tras adoptar → debe avisar | ❌ **silencio total** |
| 6 | commit cuyo mensaje nombra otro unit → DENY | ✅ con control positivo |
| 7 | `ingest` con transcript previo al run → reporta lo descartado | ✅ |
| 8 | repo sin `git init` → ¿degrada o revienta? | ⚠️ degrada, **en silencio** |

**Ningún fix previo quedó invalidado.** Los ocho siguen valiendo en esta forma. Lo que la réplica
encontró es distinto: dos silencios nuevos y un falso verde.

---

## 1 · `init` — ✅

```
project="uiclone" unit="US", 4 domain(s) detected, graphify found, engram found
```

- `unit.pattern: 'US-\d{3}'`, derivado **del backlog** (no hay ramas de las que derivarlo).
- `unit.plan: docs/backlog.md`, `locator: '### {id}'`.
- Los 4 dominios completos, cada uno con su `rules:` apuntando a su `AGENTS.md`.
- `gates.qa.checks: []` **reportado como vacío en voz alta**, en el bloque de cierre del archivo:
  *"NO check command was found — gates.qa.checks is empty, which means the gate currently
  verifies nothing. Fill it before flipping enforcement to 'enforce'"*.

## 2 · Gate sin checks + presupuesto excedido — ✅

La rama donde vivía la regresión. Con `gates.qa.checks: []`, `on_exceed: block` y 2.4M tokens
medidos contra un techo de 1.0M:

```
deny — batten: US-001 is over budget. budget.on_exceed=block.
  ! tokens       2415000 of 1000000
```

**Control positivo** (el mismo payload con el techo subido a 9M): no deniega, y en su lugar
aparece el aviso que la rama sin checks tenía guardado —

```
batten (warning — not blocking): gate "qa" declares no checks, so US-001 was approved on the
agent's word alone — nothing was run to verify it.
```

Las dos mitades del fix `53227e8` quedan probadas de una vez: el aviso **no tapa** la denegación,
y la denegación **sigue existiendo** en la rama sin checks.

## 3 · Dos units en la misma fase — ✅

`US-030` y `US-031`, las dos en `build`, cada una con su subagente:

```
US-030  g-US-030-…:p-build · US-030-…:p-build · US-030-…:n-fe-agent · header   (4 nodos, 1 arista)
US-031  g-US-031-…:p-build · US-031-…:p-build · US-031-…:n-be-agent · header   (4 nodos, 1 arista)
```

Ids de nodo con su run adentro, cero contaminación cruzada, ningún subagente perdido. `a1a4075`
se sostiene.

## 4 · `git -c … commit` — ✅

Entra al gate con las dos formas de opción interpuesta:

- `git -c user.name=CI -c user.email=ci@x.io commit -m …` → DENY
- `git -C . commit -m …` → DENY

**Control positivo:** `git -c user.name=CI … status --short` → silencio. El patrón ve el commit
sin sobre-capturar todo lo que empiece con `git -c`.

## 5 · Primer commit tras adoptar — ❌ SILENCIO

**El único fallo, y es el escenario más probable de todos: el primer commit de alguien que acaba
de instalar batten.**

DB vacía, ningún run abierto, el mensaje no nombra ningún unit, no hay rama que lo nombre
(no es repo git):

```
$ echo '{…"command":"git commit -m \"primer commit del equipo\""}' | batten hook PreToolUse
$                                    ← nada. exit 0.
```

Reproducido en `enforcement: enforce` **y** en `enforcement: report` (el default de `init`).

El origen es [`internal/hooks/hooks.go:379`](../../internal/hooks/hooks.go#L379):

```go
return nil, nil // nothing open and nothing to attribute: SessionStart carries this one
```

**La excusa es sólo medio cierta.** `SessionStart` sí lo dice, y lo dice bien:

> No run has been opened yet, so **the commit gate is not governing anything**. A commit right
> now lands unverified.

Pero lo dice en `additionalContext` —que va al **contexto del modelo**, no a la pantalla del
usuario—, una sola vez, al arrancar la sesión. En el momento del commit, que puede ser doscientos
turnos después, **ni el humano ni el modelo reciben nada**. Y el silencio de `batten hook` sale 0
por seis razones distintas, así que es indistinguible de un ALLOW.

Es literalmente el modo de falla que el propio comentario del código, treinta líneas más arriba,
dice estar arreglando: *"Silence from this hook is indistinguishable from approval, so the user
concludes the gate is working. It is not."*

**Va al §4.3 (fail-open ruidoso), que hasta ahora sólo cubría `main.go:163-197`.**

> Caso hermano, **no verificado**: cuando hay exactamente UN run abierto pero pertenece a otra
> sesión, `activeUnit` devuelve `""` y `len(open) > 1` es falso, así que se cae al mismo
> `return nil, nil`. Es alcanzable leyendo el código; no logré producirlo desde el CLI, porque
> la adopción de sesión ocurre dentro del propio camino del commit. Queda anotado como hipótesis,
> no como hallazgo.

## 6 · Mensaje que nombra otro unit — ✅

Sesión atada a `US-001` (con veredicto); `US-002` abierto y sin veredicto:

| commit | resultado |
|---|---|
| `git commit -m "US-002: …"` | DENY — *US-002 has no verdict envelope* |
| `git commit -m "US-001: …"` | DENY — *US-001 is over budget* (control positivo) |
| `git commit -m "arregla un typo"` | DENY por US-001 — cae en la atadura de sesión |

Tres caminos, tres razones distintas. El mensaje manda sobre la atadura, y la atadura sigue
funcionando cuando el mensaje no dice nada.

## 7 · `ingest` con transcript previo al run — ✅

```
US-001: +2 requests, 2.4M tokens total, $14.38 imputed
  2 request(s) in this transcript predate the run and were NOT counted (1.8M tokens, $10.75).
  They belong to the session, not to US-001, which opened at 16:27:36.
```

La valla temporal descarta lo anterior al run y **dice qué descartó**, en tokens y en dólares.

## 8 · Repo sin `git init` — ⚠️ degrada, en silencio

No revienta: `doctor`, `phase`, `runs`, `show`, `canvas` e `ingest` corren todos con exit 0. Pero
hay dos cosas que se callan.

### (a) El ancla se declara y no existe

`phases[].anchor: git_sha` está en el `batten.yaml` que **el propio `init` escribió** para este
repo. No hay repo git, así que no hay SHA:

```
$ batten phase US-001 build
US-001 -> phase build          ← exit 0, sin una palabra sobre el ancla

$ batten show US-001
US-001  run=…  status=running  phase=build  base=      ← vacío
```

Nadie avisa que el ancla que el spec declara no se pudo grabar. Es la décima instancia del patrón
de §2.1 —declarar una capacidad que no se impone— sólo que acá el que declaró fue `init`.

### (b) `doctor` da un ✓ verde a hooks que no pueden estar instalados

```
✓ graphify git hooks installed (auto-rebuild + graph.json merge driver)
```

En un directorio **sin `.git`**. La causa está en
[`cmd/batten/main.go:1476`](../../cmd/batten/main.go#L1476): `graphHooks` corre
`graphify hook status` y concluye que todo está bien **si no encuentra las cadenas de fallo**:

```go
missingDriver := strings.Contains(s, "merge driver: not registered")
missingHooks  := strings.Contains(s, "post-commit: not installed")
if !missingDriver && !missingHooks {
    fmt.Println("  ✓ graphify git hooks installed …")
```

Fuera de un repo git, graphify imprime `Not in a git repository.` y **sale 0**. Ninguna de las dos
cadenas aparece, así que batten reporta el ✓.

Es el mismo error de razonamiento que la regla del silencio: **inferir el éxito de la ausencia de
un fracaso conocido**. Un `doctor` que da verdes falsos es peor que uno que no chequea, porque es
lo primero que corre alguien cuando algo ya falló.

**Va al §5.5 (`doctor` clínico), junto a los hallazgos #58 y #60.**
