# Plan de publicación — lo que queda

> Escrito **2026-07-30**, rama `refinamiento-plugin`, HEAD `c47d57a`, árbol limpio, suite verde en
> **17 paquetes**, matrices **41/41** y **26/26**. **67 commits adelante de `origin/main`, sin
> divergencia. Sin pushear.**
>
> Sucede a [`plan_mejora.md`](plan_mejora.md), cuyos cuatro bloques están cerrados, y absorbe lo
> único que seguía vivo de `adopcion_y_esencia.md` y `gentle_ai.md` — los dos borrados en el mismo
> commit que creó este archivo, con su contenido aplicado o rescatado acá (§4 y §5).
>
> **Este plan no es de motor.** El motor está. Es del camino por el que otra persona lo recibe, y
> de las dos decisiones que no son mías.

---

## 0. Qué cambió desde el cierre del bloque 4

Una auditoría de la **distribución** —no del motor— encontró cinco blockers de instalación. Están
los cinco cerrados, cada uno con un test que falla contra el commit anterior:

| | qué estaba roto | commit |
|---|---|---|
| A1 | el binario se descargaba a `${CLAUDE_PLUGIN_DATA}/bin` y los 8 hooks, el MCP, el `batten` pelado de los comandos y `doctor` nombran `${CLAUDE_PLUGIN_ROOT}/bin/batten` | `f2f289c` |
| A2 | los seis `.sh` trackeados 100644 → `Permission denied` en macOS y Linux | `76b1e0a` |
| A3 | Windows sin Git Bash no tenía ningún camino de instalación | `60b35e7` |
| A4 | los comandos corrían `batten` pelado y seguían adelante con `command not found` | `ad56e0d` |
| A5 | el camino de release estaba verificado **leyéndolo** | `8ffc3fc` |

Y dos blockers **que la auditoría no tenía**, que salieron de ejecutar lo que ella leyó:

| | qué estaba roto | commit |
|---|---|---|
| — | **CI estaba rojo en `dc404c2`**: el schema publicado rechazaba el `batten.yaml` del propio proyecto | `786108b` |
| — | el grafo de código no conocía tres de diecisiete paquetes, con `query_before_read` activo | `2e840ab` |

El resto del contexto está en el [CHANGELOG](../../CHANGELOG.md); acá solo lo que **falta**.

---

## 1. BLOQUE 0 — las dos decisiones que son tuyas

**Nada de lo que sigue avanza hasta que estas dos se decidan.** No toqué un byte de ninguna.

### 1.1 — `docs/field-test/`: limpiar hacia adelante o purgar historia

El directorio no describe la réplica sintética: describe el proyecto privado. Inventario exacto,
verificado archivo por archivo:

| archivo | ¿público hoy? | qué filtra |
|---|---|---|
| `TEST-MATRIX.md` | **sí** | línea 31 nombra `$HOME/Proyectos/MNA/proyecto_ui` — la ruta real, en un comentario que dice "nunca esta ruta" |
| `HANDOFF.md` | **sí** | "4 domains each with their own AGENTS.md, 47 skills, 9 custom agents, a backlog of US-001..US-0NN" |
| `ARTIFACT-PLAN.md` | **sí** | 27 menciones de `proyecto_ui`, 38 de `US-0NN` |
| `dimensions.json` | **sí** | 23 `proyecto_ui`, 1 `MNA`, repro notes con la ruta real |
| `verdicts.json` | **sí** | 30 `proyecto_ui`, 4 `MNA` |
| `FINDINGS-RAW.md` | **sí** | 2 `proyecto_ui` |
| `unverified.json` | **sí** | 1 `proyecto_ui` |
| **`verified.json`** | **NO** | 9 `proyecto_ui`, 1 `MNA`, 97 `US-0NN` |
| `REPLICA-UI.md` | **NO** | sintético legítimo (descartado como fuga) |

**Siete de nueve ya son públicos desde `b38fd5a`.** Los dos que no lo son se publicarían con el
merge a `main`, y uno de ellos —`verified.json`— filtra.

Una corrección al hallazgo original: **los SHAs de `HANDOFF.md` son commits de ESTE repo**
(`53227e8`, `24d8e4c`, `a1a4075` existen acá), no del proyecto privado. Esa parte del hallazgo era
falsa.

Las dos opciones, con su costo real:

- **Limpiar hacia adelante.** Un commit que reescribe los nueve archivos con la réplica sintética
  como sujeto. Barato, no reescribe historia, no rompe clones. **Lo que ya es público sigue siendo
  recuperable del historial** — que es lo que hay que aceptar explícitamente para elegir esta.
- **Purgar historia.** `git filter-repo`, force-push, y todo clon existente queda inválido.
  Es la única que borra de verdad. Cuesta caro y **se vuelve mucho más cara después de publicar un
  release**: un tag firmado y unos assets descargables congelan el contenido, y GitHub cachea e
  indexa lo que ya sirvió.

> **Recomendación:** decidir esto ANTES del §2. Si la respuesta es purgar, purgar primero y taggear
> después; el orden inverso no se puede deshacer.

### 1.2 — El push, el tag y el release quedaron en espera a propósito

No es prudencia genérica, es este orden concreto:

1. **Publicar congela el contenido.** Después del release, purgar historia sigue siendo posible pero
   deja un release cuyos assets ya circularon y un `main` reescrito bajo ellos.
2. **El merge a `main` publica `verified.json` y `REPLICA-UI.md`**, que hoy no son públicos. Es
   decir: el merge es *parte* de la decisión 1.1, no un paso independiente.
3. `marketplace.json` no fija ref, así que `/plugin marketplace add` clona la rama default: **hay que
   mergear a `main` antes de taggear** o el usuario recibe binario nuevo con prompts viejos
   (`batten-build.md` cambió +72 líneas).

---

## 2. BLOQUE 1 — la secuencia de publicación

Ejecutar en este orden, y solo después de §1.

```bash
# 0. la verificación que sí se puede hacer antes de publicar (ya corrida: 6/6 en verde)
scripts/release-check.sh v0.1.0-beta.1

# 1. main primero, porque el marketplace clona la rama default
git checkout main && git merge --ff-only refinamiento-plugin && git push origin main

# 2. el tag. La v es obligatoria: release.yml dispara con tags: ['v*']
git tag -a v0.1.0-beta.1 -m "..." && git push origin v0.1.0-beta.1

# 3. mirar el workflow, no asumirlo
gh run watch

# 4. `prerelease: auto` marca el tag -beta como prerelease, y GitHub excluye los prereleases de
#    `latest` — que es de donde bootstrap descarga. Sin esto, 404 en las seis plataformas.
gh release edit v0.1.0-beta.1 --prerelease=false --latest --notes-file <notas>

# 5. la prueba que solo existe después de publicar
for p in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64 windows_arm64; do
  curl -sIL -o /dev/null -w "%{http_code} $p\n" \
    "https://github.com/ArthurZizumbo/batten/releases/latest/download/batten_${p}.tar.gz"
done   # los seis en 200
```

**Decisión ya tomada, respetarla:** no se toca `.goreleaser.yaml`. `prerelease: auto` se queda; el
`gh release edit` posterior es el arreglo.

### 2.1 — Y después, lo único que cierra el círculo

Instalación real de marketplace, **en Windows y en un Unix**, desde el release publicado — no desde
un checkout. Es la primera vez que el camino existiría de punta a punta, y es lo que convierte
"verificado" en "instalado".

Qué mirar en esa instalación, en este orden:

1. `${CLAUDE_PLUGIN_ROOT}/bin/batten[.exe]` existe y corre.
2. `batten doctor` dice `✓ installed binary` con la versión del tag.
3. Un `git commit` sin veredicto se **deniega** (control positivo: sin esto el PASS no prueba nada).
4. Borrar `$ROOT/bin` y abrir sesión nueva: restaura del caché **sin red**.

---

## 3. BLOQUE 2 — la brecha de cadena de suministro

**Rescatado de `gentle_ai.md`, y es lo único que seguía vivo de ese documento.**

`bootstrap.sh` y `bootstrap.ps1` descargan un binario de 14 MB y **no verifican nada**. GoReleaser
ya publica `checksums.txt`; nadie lo lee. gentle-ai v2.2.0 verifica con minisign antes de reemplazar
el binario, con tope de tamaño y estados que fallan cerrado.

Qué hacer, en dos pasos de costo muy distinto:

- **Paso 1, barato y ya posible: verificar el sha256 contra `checksums.txt`.** El archivo ya se
  publica en cada release. `sha256sum -c` en el `.sh` y `Get-FileHash` en el `.ps1`. Cubre corrupción
  en tránsito y sustitución de asset; no cubre un release comprometido.
- **Paso 2: minisign.** Requiere generar y custodiar una clave, y publicar la pública. Es lo que
  cubre el release comprometido.

**El modo de falla correcto acá es el contrario al del resto del bootstrap:** una firma que no
verifica **no** instala. Un binario no verificado es peor que ningún binario, porque los hooks lo
invocarían. Es la única parte del bootstrap que falla cerrado, y hay que decirlo en el comentario
para que nadie la "arregle" después.

Test: servidor local que sirve un archive **con el hash cambiado** → no se instala nada, `$ROOT/bin`
queda vacío, y el mensaje nombra la causa. El seam `BATTEN_BOOTSTRAP_BASE_URL` que ya existe alcanza.

---

## 4. BLOQUE 3 — lo rescatado de `adopcion_y_esencia.md`

Ese documento evaluaba un estudio de mercado y sus cinco propuestas. **Casi todo se aplicó.**
Verificado contra el código, no contra la intención:

| propuesta | veredicto de entonces | hoy |
|---|---|---|
| #1a reposicionar como habilitador de paralelismo | adoptar | ✅ README §"The write-set guard — what makes parallel fan-out safe" |
| #1b OCC con autorreparación por LLM | **rechazar** | ✅ rechazada — ver §4.1 |
| #2 cero configuración | adoptar | ✅ `batten demo` no toca nada tuyo; `init` arranca en `report` |
| #3 visual en vivo (versión barata) | adoptar | ✅ `internal/canvas/html.go`, `batten canvas --html` |
| #4 generador de PR | la mejor | ✅ `batten pr` con DAG Mermaid, evidencia y cobertura de criterios |
| #5a activar campos declarados | obligatorio | ✅ de 16 a 7, con cuatro guards que lo sostienen |
| #5b fail-closed | **rechazar → fail-open ruidoso** | ✅ implementado con el sobre tipado |
| §6.1 el reporte antes que la negación | agregado | ✅ `batten report`, y el README lidera con `demo` |
| §4.3 un número medido de terceros en el titular | agregado | ✅ el README abre con el 78 % y arXiv:2607.07405 |

**Queda vivo esto, y nada más:**

### 4.1 — Las dos decisiones que no hay que re-litigar

Se guardan acá porque el documento que las argumentaba se borra, y porque las dos van a volver a
proponerse — son ideas que suenan bien:

- **Concurrencia optimista con autorreparación por LLM: rechazada.** Poner al modelo a juzgar si su
  propio conflicto de escritura "importa" reintroduce exactamente la falla que el gate mata, en la
  otra mitad del producto. El 78 % de fallas silenciosas del paper es la medición de por qué.
  *Lo que sí es adoptable* es extender `advise()` a colisiones de baja severidad — pero que la
  severidad la decida una regla, no el modelo.
- **Fail-closed: rechazado.** SQLite bajo contención devuelve `SQLITE_BUSY`; con fail-closed eso
  deniega **cada** llamada a herramienta hasta que se libere. Una herramienta de gobierno que
  inutiliza la sesión el día que la base está ocupada se desinstala esa misma tarde. La tercera
  opción —fail-open **ruidoso**— es la que está implementada.

Y de `gentle_ai.md`, tres rechazos de la misma clase: **Receipt-Driven Development** como marco
(batten ya tiene su envelope; adoptar vocabulario ajeno sobre el mismo mecanismo agrega conceptos sin
agregar imposición), **enrutamiento de modelos por fase** (batten no orquesta, a propósito;
`models.*` se sacó del spec por eso), y **Strict TDD Mode** (lo que batten inyecta sale del
`batten.yaml` del usuario, no de su opinión).

### 4.2 — Medir antes de rediseñar: ¿cuánto se bloquean los subagentes?

batten no tiene **ninguna** medición de cuánto se bloquean entre sí los subagentes en la práctica.
[S-Bus](https://arxiv.org/pdf/2605.17076) reporta que los agentes **sobre-declaran su uso de
recursos entre 32 % y 49 %**. Si batten sobre-declara parecido, el bloqueo por write-set es real y
vale medirlo; si no, cualquier rediseño de la disjunción resuelve un problema inventado.

**Es barato:** los datos ya están en la base (los claims, las denegaciones con `rule = write_set`, y
qué archivos del write-set se tocaron de verdad — `scan-diff` ya calcula lo último). Un número en
`batten measure` o en `report`: *"de N archivos reclamados, M se escribieron"*.

Y encaja con el principio #2: hoy batten no puede contestar esa pregunta sobre sí mismo.

### 4.3 — El diferenciador que no está contado

batten es el único que mide **porcentaje de la ventana rodante de suscripción**. Todo el ecosistema
de observabilidad de costo (Langfuse, OpenTelemetry, ccusage, CloudZero) mide **dólares de API**, que
es la métrica equivocada cuando el costo marginal de un token es cero. `batten statusline` es el
único sensor local de cuota que existe.

Está construido y **no está contado como diferenciador** en ningún lado de la comunicación.
Cuesta un párrafo del README.

---

## 5. BLOQUE 4 — las brechas conocidas del field test

**14 de los 52 hallazgos confirmados siguen abiertos** (eran 15: el #1 se cerró con
`spec.UnknownKeys`). Las reproducciones están en `docs/field-test/verified.json` — cuyo destino
depende de §1.1.

El que más importa:

- **#4 — `batten claim` solo busca colisiones dentro de su propia corrida.** Una segunda corrida
  puede reclamar un archivo que el agente de otra ya posee, recibir *"any other agent writing them is
  now denied"*, y después el hook deniega a **ambos** dueños declarados. Los worktrees lo resuelven
  estructuralmente, pero nada exige, crea ni sugiere un worktree, así que en un checkout único se
  reproduce tal cual.

El resto, en una línea cada uno:

- #7 — un claim fuera de la raíz del repo se acepta con la misma falsa seguridad.
- #43 — el write-set guarda y reporta la ruta case-folded: `useTrace.ts` vuelve como `usetrace.ts`.
- #16, #50 — el camino feliz documentado omite el paso `batten check` que un gate con `checks:` exige.
- #24 — una fase que difea desde un ancla ausente avisa en runtime, pero `doctor` no.
- #23, #28 — `batten runs` no imprime id, hora de inicio ni antigüedad, y pierde la marca de "checks corridos".
- #34 — `measure` imprime encabezado de headroom en un repo que nunca declaró compresión.
- #47 — la lista de la TUI rotula 113 % como "quota" mientras el panel de detalle dice 17,0 % de lo mismo.
- #59 — `batten init` no escribe entrada de `.gitignore` para `.batten/`.
- #27 — un veredicto cuya evidencia son objetos en vez de strings falla con un error crudo del decoder de Go.
- #6, #60 — una escritura cruzada por heredoc de `python` o por un target de Makefile sigue siendo invisible al guard de Bash. Es un límite declarado, no un descuido: `batten scan-diff` es el complemento a posteriori.

---

## 6. BLOQUE 5 — lo que ningún trabajo interno cierra

- **batten nunca fue adoptado por un proyecto ajeno**, con gente que no lo escribió. Es la única
  brecha que no se cierra escribiendo código, y es la razón por la que la versión se llama `beta`.
- **El formato de transcript que batten parsea no es una API pública.** Cuando se rompe, batten
  reporta el conteo como no disponible en vez de adivinar —correcto— pero el ledger puede quedarse
  ciego sin aviso. *Mitigación posible y barata:* que `doctor` diga cuándo fue la última vez que un
  transcript se parseó con éxito.
- **Falta el GIF del README.** Los `.tape` están escritos y verificados en contenido
  (`docs/tape/demo.tape`, `docs/tape/tui.tape`); falta instalar vhs + ttyd + ffmpeg y grabarlos.

---

## 7. Anotado, no programado

Candidatos reales que no entran en este ciclo, con el motivo:

- **`cmd/batten/main.go` tiene 2387 líneas y 47 comandos.** Es el olor de capa horizontal que
  describe el documento de Gentleman Programming, y la lectura correcta —un archivo por comando,
  vertical slice— es válida. No es lo que se hace a tres commits de un release: es un cambio grande
  sin comportamiento observable, o sea todo el riesgo y ninguna evidencia.
- **`graphify-out/` es el 40 % de los bytes trackeados.** El grafo está fresco y es correcto; lo que
  queda por decidir es si `graph.html` (805 KB, derivable de `graph.json`) merece estar versionado.
- **`docs/general/plan_mejora.pdf`** (813 KB) está en el árbol pero **no** trackeado: `.gitignore`
  ignora `*.pdf` a propósito. No hay nada que hacer; queda dicho para que nadie lo "arregle".

---

## 8. Correcciones a los hallazgos reportados

Tres hallazgos de la auditoría **cambiaron de forma al verificarlos**, y quedan corregidos acá para
que no vuelvan a citarse como estaban:

1. **"El README enlaza DESIGN.md como documento primario" — falso.** El README no lo enlaza en
   absoluto. El único enlace entrante es de `ROADMAP.md`. (`DESIGN.md` sí decía `loom` 37 veces, y
   ahora lleva un encabezado histórico con la tabla de traducción en vez de una reescritura, porque
   es un documento fechado y reescribirlo hacia atrás lo volvería una falsificación.)
2. **"El grafo tiene el módulo pre-rename" — falso.** Se regeneró en `8e038b5`, el mismo commit que
   renombró el módulo. Lo real era peor y más concreto: 74 commits de atraso y cero nodos para
   `internal/install`, `internal/render` e `internal/plan`.
3. **"`HANDOFF.md` tiene un SHA real (del proyecto privado)" — falso.** Los SHAs son commits de este
   repo.

Y tres trampas que solo aparecieron **ejecutando**, anotadas porque van a volver:

- **`grep -v` en un guard de CI.** `git ls-files -s '*.sh' | grep -v '^100755' && exit 1` falla el
  step con el árbol **sano**: `grep -v` sale 1 cuando no encuentra nada y `bash -e` toma ese 1 como
  el resultado del step. Capturar primero, decidir después.
- **`tar | grep -q` bajo `set -o pipefail`.** Reporta FALLA cuando el check tiene ÉXITO: `grep -q`
  sale al primer match y cierra el pipe, `tar` recibe SIGPIPE, y `pipefail` toma el código de `tar`.
  Los seis archives del release salieron reportados como rotos por un test que estaba pasando.
- **`graphify update .` sin `--code-only`** indexa documentos y mete `docs/field-test` dentro de
  `graph.json` (328 referencias, verbatim) — y el guard de rutas personales de `ci.yml` **excluye**
  `graphify-out`. Un JSON generado de 2 MB es irrevisable en un diff: es el único lugar del repo
  donde algo privado puede entrar sin que ningún humano ni ningún check lo vea. De ahí
  `.graphifyignore`; usar siempre `graphify . --code-only` + `cluster-only --no-label`.

---

## Fuentes

- [Reason Less, Verify More](https://arxiv.org/html/2607.07405v1) — arXiv:2607.07405, el 78 % y el triple de fiabilidad
- [S-Bus](https://arxiv.org/pdf/2605.17076) — arXiv:2605.17076, sobre-declaración de recursos del 32–49 %
- [gentle-ai](https://github.com/Gentleman-Programming/gentle-ai) — v2.2.0, verificación de firma con minisign
- [graphify](https://github.com/Graphify-Labs/graphify) — `GRAPHIFY_HOOK_STRICT` opt-in, el patrón de nudge por default
