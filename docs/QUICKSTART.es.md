# Quickstart — adoptar batten en un repo que no lo tiene

> [English](QUICKSTART.md) · **Español**

> Cada comando y cada bloque de salida de abajo fue capturado de una corrida real sobre un repo
> real construido desde un directorio vacío. Nada de acá es ilustrativo.

El repo de demo es `taskly`: un proyecto Go chico con dos dominios (`api/`, `store/`), un
`AGENTS.md` por dominio declarando un invariante cada uno, un `Makefile`, y un backlog en
`docs/backlog.md` que lista `US-001`..`US-005`. Un invariante importa para lo que sigue:

> `store.ErrNotFound` mapea a 404, nunca a 500.

## 1. `batten init` — leer el repo, proponer un spec

```
$ batten init
wrote batten.yaml — a working draft in report mode (gates warn, don't block).
  project="taskly" unit="US", 2 domain(s) detected, graphify found, engram found
Next:
  1. fill the invariants (the TODOs) — the highest-value part of the file
  2. run: batten doctor
  3. flip enforcement: enforce when you trust the gates
```

Sacó `unit: US` de los encabezados del backlog, no de un nombre de rama — el repo está en `main` y
nunca tuvo una rama de feature. El spec que escribió dice de dónde salió:

```yaml
unit:
  name: US
  pattern: 'US-\d{3}'
  plan: docs/backlog.md
  locator: '### {id}'
```

Los comandos de check se tomaron verbatim del `Makefile`, y el spec lo dice en un comentario de
encabezado en vez de fingir que los inventó:

```yaml
# This repo ALREADY has a process. The spec below should agree with it, not replace it:
#   Makefile (build) — the check commands come from here VERBATIM — never invent one
#   api/AGENTS.md (agent-rules) — per-directory rules — this boundary is probably a fan-out axis
#   store/AGENTS.md (agent-rules) — per-directory rules — this boundary is probably a fan-out axis
```

Además arranca en **modo report** —los gates avisan, no bloquean— y termina el archivo con las
decisiones que no pudo tomar por vos:

```
# - invariants are empty — fill each domain's invariants with the rules a reviewer would catch
# - gates.qa.checks were taken from your build files; confirm they are the right pre-commit checks
# - enforcement starts at 'report' (gates warn, don't block) — flip to 'enforce' when trusted
```

**Hacé los TODOs antes de seguir.** Llená los `invariants` de cada dominio desde su `AGENTS.md`, y
después pasá a `enforcement: enforce`. El resto de este recorrido asume las dos cosas.

## 2. `batten doctor` — ¿este repo está gobernado de verdad?

```
$ batten doctor
✓ .../taskly/batten.yaml — project "taskly", unit "US", 3 phases, 2 domains
✓ close gate: phase "close" requires verdict "ok" on any gate
✓ enforcement: enforce — gates block
✓ store: .../state.db
✓ graph: graphify (on PATH)
  ⚠ no graphify-out/graph.json yet — run: graphify . --code-only
· memory: engram (via MCP; batten does not store episodic memory)
$ echo $?
0
```

`doctor` sale distinto de cero ante un spec que considera inválido, así que es seguro ponerlo en CI:

```
$ batten doctor
✗ .../batten.yaml: invalid spec:
  - unit.name is required (the work-item noun: US, ticket, issue...)
  - phase "verify" references unknown gate "nope"
$ echo $?
1
```

## 3. Commitear antes de abrir una corrida — batten dice que no te está gobernando

Este es el estado en el que un recién llegado está todo su primer día, y antes era silencioso.

```
$ git checkout -b feature/US-002-missing-task-404
$ git commit -m "feat(US-002): 404 for a missing task"

batten (warning — not blocking): batten: this commit is NOT gated. US-002 has no run on
record, so there is no verdict to check and nothing was verified.
Open one with `batten phase US-002 build` — the gate starts governing from there.
```

Avisa en vez de denegar, a propósito: batten no puede verificar lo que nunca se declaró, y negar
todo commit en un repo que no arrancó ninguna corrida solo lograría que lo desinstalen. Pero nunca
te deja creer que hay un gate corriendo cuando no lo hay.

## 4. `batten phase` — abrir la corrida, grabar el ancla

```
$ batten phase US-002 build
anchor: US-002 base SHA = ca015de
US-002 -> phase build
```

El ancla es el punto desde el que diffea toda fase posterior. No `HEAD~1`, no "los últimos commits"
— un SHA grabado, para que una revisión tres días y nueve commits después siga acotada al trabajo de
este unit.

Ahora escribí el código. En el demo: `api/handler.go` mapea `store.ErrNotFound` a 404, más un test
que lo verifica.

## 5. Commit sin veredicto — denegado

```
$ git commit -m "feat(US-002): 404 for a missing task"

DENY: batten: US-002 has no verdict envelope. Run the "verify" phase before committing.
To proceed anyway (recorded in the audit log): batten override US-002 --reason "..."
```

Siempre hay una salida. Queda registrada.

## 6. Solo el veredicto del revisor — sigue denegado

```
$ batten phase US-002 verify
$ batten verdict --unit US-002 --file v.json
verdict recorded: US-002 qa=ok (3 evidence)

$ git commit -m "feat(US-002): 404 for a missing task"

DENY: batten: US-002 has no batten-verified pass. The gate's checks must be RUN, not asserted.
Run: batten check US-002
```

Este es el punto entero de la herramienta. Un agente —o una persona— escribiendo "los tests pasan"
en un sobre está haciendo una afirmación. El gate quiere la afirmación *y* la corrida.

## 7. `batten check` — batten corre los checks del propio gate

```
$ batten check US-002
  ✓ go build ./...
  ✓ go vet ./...
  ✓ go test ./...

US-002: OK (batten-verified). all gate checks passed (batten ran them)
```

Ahora el mismo commit pasa — en silencio, porque un commit permitido es un commit común.

```
$ git commit -m "feat(US-002): a missing task returns 404, not 500"
[feature/US-002-missing-task-404 3a1c9f2] feat(US-002): a missing task returns 404, not 500
```

**Los dos veredictos son obligatorios, y tienen que venir de productores distintos.**
`batten check` prueba que los checks declarados corrieron. El sobre del veredicto prueba que alguien
juzgó el trabajo contra sus criterios de aceptación. Ninguno reemplaza al otro:

```
$ batten check US-003          # solo existe el veredicto de batten
$ git commit -m "feat(US-003): mark a task done"

DENY: batten: US-003 has only batten's own check result. `batten check` proves the checks ran;
it does not judge whether the work meets its acceptance criteria.
Record a verdict from the verify phase: batten verdict --file v.json
```

## 8. `batten close` — y cómo se ve el registro

```
$ batten phase US-002 close
$ batten close US-002 --status ok
US-002 closed (ok). Write-set claims released — files it held are free again.

$ batten show US-002
US-002  run=US-002-1785264143887316500-6ad6e86c  status=ok  phase=close  base=ca015de
  [phase] build                    ok
  [phase] verify                   ok
  [phase] close                    ok

verdict qa=ok (agent): a missing task now returns 404; the store invariant is unchanged
  - api/handler.go:14 maps store.ErrNotFound to http.StatusNotFound
  - TestAMissingTaskIs404NotA500 asserts w.Code == 404
  - store/AGENTS.md invariant untouched: Get still returns ErrNotFound
verdict qa=ok (batten): all gate checks passed (batten ran them)
  - go build ./...: PASS (exit 0, 585ms)
  - go vet ./...: PASS (exit 0, 686ms)
  - go test ./...: PASS (exit 0, 637ms)
```

Dos veredictos, rotulados por quién los produjo. El del revisor cita los criterios de aceptación; el
de batten cita sus propios códigos de salida y tiempos.

## El control negativo — verlo denegar de verdad

Un recorrido donde todo pasa no prueba nada. Rompé el invariante a propósito: hacé que el handler
devuelva 500 donde el `AGENTS.md` del dominio dice 404.

```
$ batten phase US-003 verify
$ batten check US-003
  ✓ go build ./...
  ✓ go vet ./...
  ✗ go test ./... (exit 1)
    --- FAIL: TestAMissingTaskIs404NotA500 (0.00s)
        handler_test.go:14: missing task returned 500, want 404
    FAIL	taskly/api	0.832s

US-003: BLOCKED (batten-verified). one or more gate checks failed
the commit gate will deny until this passes.
$ echo $?
1
```

`batten check` sale **1** cuando el unit está bloqueado, así que `batten check && ...` se corta y CI
falla. Y el commit se rechaza con la razón y el siguiente paso:

```
$ git commit -m "feat(US-003): mark a task done"

DENY: batten: US-003 verdict is "blocked", not "ok". one or more gate checks failed
safe_next_step: fix the failures, then run batten check again
```

Arreglá el handler, re-chequeá, y se despeja:

```
$ batten check US-003
  ✓ go test ./...

US-003: OK (batten-verified). all gate checks passed (batten ran them)
$ echo $?
0
```

## Qué dice el ledger cuando nadie lo midió

```
$ batten budget
US-005  usage NOT MEASURED (not zero — nothing has been ingested for this run)
  · tokens       NOT MEASURABLE — no usage has been measured for this run —
                 run `batten ingest <unit> --transcript <path>`
```

No `0 tokens, $0.00`. Una corrida que nadie tarifó no gastó nada; está *sin medir*, y esas dos cosas
necesitan respuestas opuestas de quien esté leyendo. Este es el primer principio de toda la
herramienta: **nunca reportes un número que no tenés.**

## A dónde ir después

- [`README.es.md`](../README.es.md) — qué es batten y por qué el gate es un hook y no un documento.
- [`FIELD-TEST.md`](FIELD-TEST.md) — batten corrido contra un proyecto real por agentes que nunca lo
  habían visto, y los 52 defectos confirmados que volvieron. *(en inglés)*
- [`ARCHITECTURE.es.md`](ARCHITECTURE.es.md) — cómo está armado por dentro, y por dónde empezar a
  leer el código.
- `batten tui` — los mismos registros, revisables sin salir de la terminal.
- `batten canvas <unit>` — el run graph como JSON Canvas, que Obsidian renderiza.

**Una regla si vas a scriptear contra batten:** exportá `BATTEN_DB` antes de cada comando cuando
trabajes en un sandbox. Cae a tu base de datos real en el momento en que no está seteada.
