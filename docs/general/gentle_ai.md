# Lo que gentle-ai le enseña a batten — y lo que no

> Evaluación de [Gentleman-Programming/gentle-ai](https://github.com/Gentleman-Programming/gentle-ai)
> (v2.2.0 y sus release notes) contra el código de batten, **2026-07-28**.
> Cinco propuestas traídas por Arthur, más lo que salió de leer el resto.
> Cada veredicto está medido o argumentado contra el código, no contra la intención.

**Resultado: 4 aceptadas (una en forma distinta a la propuesta), 1 rechazada quedándome con su
mitad útil, 3 nuevas encontradas leyendo las notas de release.** Todo lo aceptado está
implementado y commiteado.

---

## Por qué gentle-ai vale como fuente

No es un competidor: es un **configurador de ecosistema** —instala memoria, SDD, skills y MCP
para agentes ajenos— mientras batten es un **motor de imposición** para el flujo que ya tenés.
No compiten por el mismo lugar.

Pero llegaron antes a un problema que batten tiene igual: **cómo confiar en lo que un agente dice
que hizo**. Su frase —*"trust what the system can derive, not agent narration"*— es la misma tesis
que la primera línea del README de batten dicha de otra manera. Cuando dos proyectos que no se
conocen convergen en la misma tesis, las diferencias en cómo la implementan son datos.

Su hallazgo transversal, que es el que más aportó acá: **cuando algo falla, no falles genérico.
Devolvé un estado con nombre y el comando exacto de recuperación.** batten denegaba bien y
explicaba bien en prosa; lo que no hacía era darle al bucle del agente algo sobre lo que actuar.

---

## 1. Identidad por dispositivo+inodo — ✅ ACEPTADA, como HÍBRIDO

**La propuesta:** dejar de comparar rutas como texto y derivar la identidad del archivo del
sistema operativo (`dev`, `ino`), porque `/var` y `/private/var` son el mismo archivo con dos
nombres, y NFC/NFD en APFS también.

**Lo medí antes de implementarlo** (`scratchpad/idexp`, en esta misma máquina Windows):

| caso | `normPath` | `os.SameFile` | |
|---|---|---|---|
| `real.go` vs `REAL.GO` | iguales | iguales | ya estaba resuelto |
| **hardlink** | **distintos** | **iguales** | **BYPASS REAL** |
| `sub/../real.go` | iguales | iguales | `filepath.Clean` ya lo resuelve |
| symlink | — | — | no se puede crear sin privilegios en Windows; en macOS/Linux sale gratis |
| NFC vs NFD | distintos | *el archivo no existe* | en NTFS son **dos archivos**, no un alias. Es problema de APFS/HFS+, no universal |

Así que la propuesta tiene razón en el diagnóstico y el agujero es real. **Pero tiene un límite
que no menciona, y también lo medí:**

> **Un archivo que todavía no existe no tiene inodo.** `os.Stat` falla. Y el write-set cerca
> justamente los archivos que un agente **está por escribir** — casi todos inexistentes cuando se
> reclaman.

Una identidad basada sólo en `(dev, ino)` **no puede cercar el caso para el que el guard existe**.

**Implementado como híbrido** (`a2ae987`): la ruta sigue siendo la clave primaria —el
`PRIMARY KEY (run_id, path)` que hace que la disjunción sea una restricción y no un consejo— y la
identidad del SO entra como **segunda consulta**, sólo cuando la búsqueda por ruta falló y el
archivo existe de verdad. El caso común —el guard corre *antes* de la escritura— sale gratis en la
primera línea.

**Sin dependencia nueva:** `os.SameFile` es portable y ya usa el índice de archivo en Windows y
`(dev, ino)` en Unix. Extraer los números crudos habría necesitado `golang.org/x/sys` o código por
plataforma; este repo mantiene el `go.mod` chico a propósito.

## 2. Contratos CAS-bound y target estancado — ✅ ACEPTADA

**La propuesta:** congelar el árbol candidato y abortar con `stale_target_identity` si algo cambió
entre la planificación y la ejecución.

**El agujero es real y toca la afirmación central de batten.** `batten check` prueba que los checks
declarados pasaron — los probó *sobre un árbol*, y no guardaba ningún rastro de cuál. Un
formateador entre el check y el commit deja el veredicto diciendo `batten-verified` sobre un estado
que ya no existe. *"Verificado significa que batten lo vio pasar"* deja de ser cierto si lo que se
commitea no es lo que vio.

**Implementado** (`e73a6fe`) con dos diferencias deliberadas:

- **Reporta, no congela.** batten no es dueño del index, y secuestrarlo entre fases rompería todas
  las demás herramientas que el desarrollador usa. gentle-ai puede congelar porque *es* el dueño
  del flujo de revisión; batten es un riel, no el motor.
- **Degrada honesto.** Huella vacía significa *no medible acá* — un repo que no es repo git, que es
  una forma real contra la que batten fue probado en campo. Comparar contra "no medible" sería
  inventar una denegación a partir de una ausencia: el espejo exacto de inventar un número.

**Dos veces estuvo mal, y las dos las encontró el test, ninguna la lectura.** Vale anotarlas porque
son el tipo de error que sólo aparece corriéndolo: (1) la primera versión hasheaba todo lo que git
reportaba, y los WAL/SHM de SQLite cambian en cada escritura — así que *grabar* el veredicto
cambiaba el árbol del que el veredicto hablaba; (2) filtrar los archivos sin trackear no alcanzó,
porque hay repos que **commitean** la base para compartir el historial, y ahí las escrituras de
batten salen por `git diff HEAD`.

> **batten no puede invalidar un veredicto escribiendo en su propio libro mayor.** Es la misma
> regla que sigue el replay log cuando se niega a registrar su propio fallo al registrar.

## 3. Recuperación dirigida por estado (`scope-changed`) — ✅ ACEPTADA, reencuadrada

**La propuesta:** en vez de "autoridad corrupta", devolver `scope-changed` con el comando exacto.

**El encuadre original no aplicaba tal cual**, y vale decir por qué: el ancla de batten
(`anchor: git_sha`) **no tiene consumidor** — está en la lista de campos declarados-como-futuro
(§8). Construir `batten recover` para reparar un ancla que nadie lee habría sido, otra vez,
declarar una capacidad que no hace nada.

**Pero el §2 lo volvió urgente.** La huella del target incluye HEAD, así que **desde ahora un
rebase vuelve estancado todo**. Y ahí la propuesta acierta exactamente: *"te editaron un archivo"*
y *"se movió la historia"* necesitan consejos **opuestos**. Un hash opaco único no puede
distinguirlos: sólo sabe decir "distinto".

**Implementado** (`7f255d8`): la huella guarda el commit y el árbol por separado, la denegación
dice `MOVED BASE` nombrando los dos commits y aclarando que el trabajo sin commitear está intacto,
y `batten recover <unit>` existe y dice qué le pasó al ancla vieja (desaparecida por un rebase, o
viva pero ya no ancestro — accidentes distintos con lecciones distintas).

**Lo que `recover` NO hace, y es lo que lo mantiene honesto:** no limpia un veredicto, no reabre
una compuerta, no hace pasar nada. Mueve el ancla y registra que la movió. Un comando de
recuperación que reaprobara trabajo en silencio sería exactamente el agujero que batten existe
para tapar.

## 4. Contención en Windows como transitoria — ✅ ACEPTADA

**La propuesta:** clasificar los `ERROR_SHARING_VIOLATION` / `ERROR_ACCESS_DENIED` de los
antivirus como transitorios y reintentar con backoff, en vez de fallar cerrado.

`busy_timeout=5000` ya estaba puesto (`internal/store/store.go`). Lo que faltaba era la
clasificación.

**Y el costo de no tenerla subió con el bloque 1 de este mismo plan:** el §4.3 hizo que batten
**avise fuerte** cuando no puede correr. Un escaneo de 30ms pondría *"batten did NOT run for this
tool call"* delante de alguien cuya máquina está perfecta — y un aviso que grita al lobo es un
aviso que la gente aprende a saltear. Arreglar una cosa creó el riesgo en la otra.

**Implementado** (`f788221`) con una regla explícita: **preciso antes que paciente.** Se reintenta
lo que está en la lista y nada más. Una base corrupta, un volumen de solo lectura, un error de
esquema — reintentarlos sólo demora la verdad unos cientos de milisegundos.

## 5. Kill switch limpio — ❌ RECHAZADO como comando, ✅ aceptada su mitad

**La propuesta:** un apagado determinista que quita los hooks sin fabricar aprobaciones, y que al
reencenderse revalida el repo.

**Rechazado, por dos razones y en este orden:**

1. **batten no puede quitar sus hooks.** Los registra Claude Code vía `hooks.json`; no hay forma de
   desregistrarlos a mitad de sesión. gentle-ai puede porque es el instalador.
2. **Y sobre todo, no le hace falta: `enforcement: report` YA es el apagado honesto.** Avisa en vez
   de bloquear, no fabrica ninguna aprobación, y es lo que `init` escribe por default. Un
   `batten disable` que dejara a batten mudo sería **estrictamente peor** que report — reintroduciría
   el silencio que el ítem 3 del bloque 1 acaba de sacar, y esa es la regresión más cara del
   proyecto.

**Lo que sí faltaba es la otra mitad de un kill switch que valga la pena: saber qué pasó mientras
estuvo apagado.** Eso está implementado (`f788221`): cada decisión registra el modo vigente, y
`batten report` dice cuántas fueron **sólo avisos** que pasaron igual. Sin eso, *"estuvimos en modo
report tres semanas"* no tiene registro de lo que costó.

---

## Lo que salió de leer el resto de las notas de release

Tres cosas que no estaban en las cinco propuestas.

### a) El sobre de fallo tipado — pendiente, y es el mejor candidato siguiente

gentle-ai v2.1.6 publica un *"uniform failure envelope"*: cada fallo reporta si mutó algo, si es
reintentable, si aplica, y **qué entradas hacen falta**. batten deniega en prosa — buena prosa,
pero prosa. Un bucle de agente tiene que parsear inglés para saber qué hacer.

El bloque 1 ya empujó en esa dirección sin nombrarla: la columna `rule` agrupa denegaciones por
causa, y `MOVED BASE` / `STALE TARGET` son estados con nombre que traen su comando. **Falta hacerlo
sistemático**: un código de razón y un `required_input` en cada denegación, expuestos por MCP.
Cuesta poco y es lo que más le sirve al modo desatendido.

### b) Lock de mantenimiento con alcance `git-common-dir` — para cuando entren los worktrees

v2.1.9 cierra una carrera de inodos en `flock` sobre linajes movibles, con un lock compartido
scopeado al *git common dir*. batten no lo necesita **todavía** porque no crea worktrees — pero
§5.4 del plan los tiene en el bloque 3, y el día que entren, la base compartida entre worktrees es
exactamente esa carrera. **Anotado para ese ítem, no antes.**

### c) Verificación de firma en el release — fuera de alcance, pero correcto

v2.2.0 verifica `checksums.txt` con minisign antes de reemplazar el binario, con tope de tamaño y
estados que fallan cerrado. batten descarga su binario en `bootstrap.sh` **sin verificar nada**.
Es una brecha real de cadena de suministro. No entra en este ciclo —toca el camino de release, que
según el §12 del plan está verificado leyéndolo y no ejecutándolo— pero **es la mejor razón que vi
para hacer ese release taggeado**.

---

## Lo que NO tomé, y por qué

- **Receipt-Driven Development como marco.** batten ya tiene su envelope de veredicto con
  evidencia citada. Adoptar el vocabulario de otro proyecto sobre el mismo mecanismo agregaría
  conceptos sin agregar imposición.
- **Enrutamiento por fase de modelos (el `gentle-orchestrator`).** batten **no orquesta**, a
  propósito ("los rieles, no el motor"). Y `models.tiers` ya está en la lista de campos muertos
  precisamente porque promete enrutar y no enruta. Implementar el enrutador sería cruzar el límite
  de diseño; sacarlo del spec es la decisión ya tomada.
- **Strict TDD Mode y las guías de metodología.** Es lo que hace superpowers, y el skill
  `batten-engine` lo prohíbe explícitamente: *"nunca asumas un workflow que no esté declarado
  ahí"*. Lo que batten inyecta sale del `batten.yaml` del usuario, no de su opinión.
