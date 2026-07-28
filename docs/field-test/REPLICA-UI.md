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

## Resultado: 6 de 8 pasan, 1 falla, 1 pasa a medias

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
