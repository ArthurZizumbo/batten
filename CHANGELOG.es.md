# Changelog

> [English](CHANGELOG.md) · **Español**

Lo que efectivamente aterrizó, lo más nuevo primero. Los arreglos encontrados *usando* batten
sobre sí mismo van marcados **[dogfood]** — son los que justifican el ejercicio.

El formato sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); las versiones siguen
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Sin publicar]

Nada todavía.

## [0.2.0] — 2026-07-31

El primer tag después de la beta. Minor y no patch porque cambió el build en sí: lo que el release
compila, y lo que el binario reporta sobre sí mismo, son distintos ahora.

### Arreglado

- **`-X main.version` nunca funcionó, y nada lo dijo.** `.goreleaser.yaml` siempre pasó
  `-X main.version={{.Version}}`, pero `main.version` estaba inicializada con una llamada a función
  (`buildVersion("0.1.0")`), y el linker de Go **ignora `-X` en silencio** salvo que la variable
  esté declarada sin inicializar o con una constante. Sin error y sin aviso: cada release reportaba
  lo que `debug.ReadBuildInfo` hubiera embebido, y `batten doctor` comparaba *eso* contra
  `plugin.json`. Ahora `version` es un `""` pelado y el fallback se movió a `init()`.
  `TestTheLinkerCanStampTheVersion` sostiene la línea corriendo el linker de verdad, porque el
  defecto es invisible para cualquier cosa que solo lea el código. **[dogfood]**
- **Los binarios publicados embebían las rutas absolutas del autor** —
  `C:/Users/<usuario>/…/cmd/batten/main.go`, en los seis. `-s -w` **no** las quita; solo `-trimpath`
  lo hace, así que el release se veía stripped y limpio sin estarlo. Los guards de rutas personales
  que ya existían leen *archivos* y nunca podrían ver dentro de un binario compilado.
  `TestTheReleasedBinaryLeaksNoPersonalPaths` cierra esa brecha.

### Agregado

- **Un recurso VERSIONINFO de Windows en `batten.exe`.** CompanyName, ProductName, FileDescription,
  LegalCopyright y OriginalFilename ahora aparecen en Propiedades → Detalles; un binario de Go sale
  con todos vacíos. Lo genera `scripts/gen-winres.{sh,ps1}` desde `cmd/batten/versioninfo.json`.
  Costo medido: **1024 bytes** sobre un binario de 15 MB.
  **Esto NO evita que Windows Defender lo marque, y nada afirma que lo haga** — ver *Huecos
  conocidos*. Quita una señal negativa barata; esa es toda la afirmación.
- **`scripts/set-version.sh <versión>`** — escribe la versión en los dos manifiestos, que eran los
  únicos dos lugares que una persona tenía que editar a mano. No pueden leer una variable de
  entorno: Claude Code parsea esos archivos como JSON literal, sin ningún paso de interpolación.

### Cambiado

- `scripts/build-plugin.{sh,ps1}` ahora compilan exactamente lo que compila el release —
  `-trimpath`, el recurso de versión y el mismo `-X main.version`. Un binario compilado localmente
  es la respuesta que se le da a quien sufre el falso positivo del antivirus, así que no puede ser
  un artefacto distinto.

### Huecos conocidos

- **El falso positivo no está arreglado, y no se puede demostrar que lo esté.** Medido el
  2026-07-31 con motor `1.1.26060.3008` y definiciones `1.455.440.0`: **0/10 detecciones en el build
  viejo y 0/10 en el nuevo**, con un control EICAR que confirma que el escáner estaba vivo sobre esa
  ruta. La línea base tampoco disparó, así que **el experimento no tiene poder discriminante** y el
  umbral pre-registrado (≤5/10) queda inevaluable. Lo que sí confirma, por tercera vez de forma
  independiente, es que el clasificador es estocástico.
- **batten sigue sin firmar.** Authenticode es lo único que cambia la categoría del problema, y
  tampoco lo elimina.

## [0.1.0-beta.1] — 2026-07-30

La primera versión con tag, y la primera que puede instalar alguien que no sea su autor.

Todo lo de abajo salió en este tag. Llevaba un segundo juego de notas, anterior, escrito antes de
cortar el tag; esas siguen bajo *"Lo que cambió el field test"* y se conservan como se escribieron
en vez de fusionarse, porque son el registro de un ejercicio distinto.

### Seguridad

- **La verificación sha256 se probó contra infraestructura real de GitHub, no solo contra un
  servidor local.** Ensayar el simulacro de reversión R2 del plan de publicación en un repo público
  descartable produjo el caso espejo por accidente: un release cuyo `checksums.txt` afirmaba hashes
  que los archivos no tenían. `bootstrap.sh` lo rechazó — nada instalado, caché sin sembrar,
  exit 0 — dos veces: una en el tag explícito y otra en `releases/latest/download` después de que
  el simulacro moviera ese puntero encima. La misma corrida además ejercitó la grafía BSD
  `hash *name` que escribe el `sha256sum` de Git Bash y que ningún test local había cubierto.

- **Los bootstraps verifican el sha256 del release antes de instalar nada.** Los dos scripts
  descargaban 14 MB y no chequeaban más que el binario contestara `version` — cosa que un binario
  hostil contesta encantado, y siete hooks más el servidor MCP ejecutan ese archivo. GoReleaser
  viene publicando `checksums.txt` con cada release desde antes del primer tag, para que nadie lo
  lea. Ahora lo bajan, extraen **la línea de su propio asset** (un `sha256sum -c` pelado falla
  siempre: el archivo lista los seis assets y cinco no están en disco) y comparan.

  Esta es la única parte del bootstrap que **falla cerrada**, y el código lo dice en ambos archivos
  para que nadie la "arregle" después. Un hash equivocado, un `checksums.txt` inalcanzable, un
  archivo de sumas que no lista este asset y una máquina sin herramienta sha256 son la misma
  sentencia — nadie puede responder por estos bytes — y reciben la misma respuesta: no se instala
  nada, la caché no se siembra, stderr nombra la url, el hash esperado y el que obtuvo. El script
  igual sale con 0, porque `hooks.json` despacha `bash bootstrap.sh || powershell bootstrap.ps1` y
  un exit distinto de cero ahí significa "acá no hay bash", no "la descarga vino mal".

  La consecuencia aceptada, escrita en vez de descubierta: una primera descarga corrupta deja la
  máquina **sin gate**. Eso ya es lo que deja cualquier descarga fallida, la alternativa es
  ejecución remota de código por descarga, y solo aplica a una primera instalación — desde la
  primera buena, la caché restaura el último binario *verificado* sin red.

### Cambiado

- **Un binario que desaparece después de una instalación buena ya no se restaura en silencio.** La
  rama de caché de ambos bootstraps imprimía *"restored from … (plugin update)"* — afirmando una
  causa que nunca había chequeado. Una actualización del plugin es la razón habitual de que
  `${CLAUDE_PLUGIN_ROOT}/bin` esté vacío. No es la única, y la otra es peor: **un antivirus
  poniendo en cuarentena el binario después de que instaló limpio.**

  Esto no es hipotético. Windows Defender clasificó un `batten.exe` recién compilado como
  `Trojan:Win32/Bearfoos.A!ml` en la propia máquina de este proyecto. El sufijo `!ml` es un
  veredicto de machine learning y no una firma, y se comporta como tal: builds byte-idénticos
  recibieron respuestas distintas, y un re-escaneo explícito de los mismos bytes volvió limpio —
  la forma de manual de un falso positivo sobre un binario Go sin firmar.

  batten no puede frenar a un antivirus. Quedarse callado al respecto es la parte que no debe
  hacer, porque el estado que deja una cuarentena es el que este proyecto existe para eliminar:
  los siete hooks son exec-form sobre un archivo que ya no existe, así que mueren en silencio; el
  servidor MCP no arranca; y `batten doctor` no puede reportar nada de eso, porque doctor **es**
  el binario que falta. Cada superficie que podría dar la alarma es la cosa que fue removida.

  El único sobreviviente es el bootstrap, y su única señal es la **repetición**. Una actualización
  del plugin explica una restauración; dos en un mismo día significan que algo está borrando el
  binario después de una instalación buena — y restaurar en silencio solo le devuelve los mismos
  bytes para el mismo tratamiento mientras imprime una línea tranquilizadora cada sesión. Ambos
  scripts ahora estampan la restauración, y la segunda dice qué está pasando, qué cuesta y dónde
  mirar.

- **`declaredAsFuture` está vacío: cada campo que `batten.yaml` acepta ahora tiene un lector.**
  Dieciséis entradas, después siete, ahora ninguna. La lista es el mecanismo que le hace a batten
  lo que batten les hace a sus usuarios — declarás un campo y tenés que cablearlo o dejar escrito,
  en una revisión, que estás enviando una promesa que no cumplís.

  Los últimos siete se fueron por **las dos** salidas, que es el punto:

  - `phases[].when` y `domains[].coverage` se **cablearon**. Los dos son advisory por contrato, y
    advisory no es lo mismo que no leído: el briefing de fase en SessionStart imprime ahora la
    condición y los pisos de cobertura declarados al agente parado en la fase, que es el único
    lector que un campo así podría tener. Los dos dicen *advisory* donde se imprimen, porque un
    piso mostrado sin eso se lee como un chequeo que batten hizo.
  - `resources` y `domains[].resources` se **quitaron**. El schema decía con todas las letras que
    *"the orchestrator runs it BEFORE launching and queues"*, y batten no orquesta — el mismo
    argumento que quitó `models.tiers`. Cuatro campos prometiendo una serialización que nada
    serializaba. La contención pertenece ahora a `domains[].invariants`, que viajan verbatim al
    prompt del agente: una regla que un agente lee le gana a un campo que nadie corrió.
  - `budget.on_exceed: warn` se **cableó** y `downgrade_effort` se **quitó**. Solo `block` había
    sido implementado alguna vez, así que un spec que elegía la opción más blanda se comportaba
    exactamente igual que uno sin techo — y `on_exceed: warn` es lo que `batten init` escribe por
    defecto, lo que dejaba a cada repo recién adoptado en la rama muerta. Qué severidad aplica lo
    decide el **spec**, nunca el modelo y nunca el tamaño del exceso.

  **Migración:** un spec que todavía lleva `resources:` sigue cargando — batten no rompe un repo
  por una clave que dejó de leer — y `batten doctor` la reporta como clave desconocida.
  `on_exceed: downgrade_effort` no carga, y el error nombra la remoción en vez de tratarla como un
  typo.

### Agregado

- **`batten scan-diff` ahora conserva su contraste, y `batten measure` lo reporta.** El comando ya
  computaba el único número que nadie más en este ecosistema reporta — cuánto sobre-declara un
  write-set declarado a mano — y lo escribía a stdout, donde moría. El denominador estaba en la
  base de datos desde v1 (`writesets` nunca borra una fila); el numerador se computaba y se
  tiraba. La migración **v12** agrega una tabla `scans`, una fila por escaneo, y a `batten measure`
  le crece un bloque *write-sets*: la mediana de `unused/claims` sobre las corridas escaneadas,
  con el N sobre el que se calculó.

  Tres reglas evitan que se adule a sí mismo. Una corrida que reclamó rutas y nunca fue escaneada
  se lee **NOT MEASURED**, jamás 0 % — si no, la mediana describe las corridas que a alguien se le
  ocurrió chequear, y esas son justo las que ya preocupaban a alguien. La mediana es sobre
  corridas, tomando el escaneo más nuevo de cada una, para que una corrida escaneada seis veces no
  cuente seis veces. Y `unused` está rotulado como **cota superior** en todos lados donde se
  imprime: mezcla "reclamó de más" con "el archivo legítimamente no necesitaba cambio", así que
  elige entre acciones en vez de medir deshonestidad.

  `batten scan-diff --strict` está ahora en los `gates.checks` de este mismo repo. Ese es el
  mecanismo de acumulación, no un adorno: nadie se acuerda de correr scan-diff a mano, así que sin
  eso las ≥10 corridas escaneadas que necesita el umbral pre-registrado no llegarían nunca.

### Quitado

- **`docs/field-test/` se retira del árbol** — nueve archivos, 1.2 MB, en la historia de git de acá
  en más. Contenía la materia prima del field test: 63 veredictos con reproducciones por hallazgo,
  evidencia verbatim y controles positivos, los 80 hallazgos tal como se presentaron, y los
  retornos por dimensión. Además describía el **repositorio privado** sobre el que se modeló la
  réplica y no la réplica en sí, y un documento sobre el codebase de otro no es publicable con
  ninguna cantidad de reescritura.

  Lo que queda público es la parte que se puede chequear: el análisis en
  [`docs/FIELD-TEST.es.md`](docs/FIELD-TEST.es.md) ([en](docs/FIELD-TEST.md)), el estado de los
  hallazgos bajo *Brechas conocidas*
  más abajo, y la evidencia **ejecutable** — `scripts/replica-ui.sh` reconstruye el fixture desde
  cero y `scripts/matrix-replica.sh` corre 41 aserciones sobre él. Lo que se pierde, dicho en vez
  de omitido: los pasos de reproducción por hallazgo ya no son públicos.

  La historia **no** se purgó, y la razón queda escrita para que no se re-litigue: un
  `filter-repo` renumera cada SHA, y este proyecto cita commits por SHA a lo largo de su plan, de
  este changelog y de sus notas guardadas. Siete de los nueve archivos llevaban meses públicos,
  así que lo que una purga compraba era angosto y lo que costaba era el sistema de referencias
  entero.

  Tres exclusiones ahora muertas se fueron con él, en vez de quedar atrás: el guard de tokens
  trackeados en `ci.yml` e `internal/install` corre ahora **sin ninguna exclusión** — sin ruta,
  sin `-I` — y sigue verde. Una exclusión que sobrevive a su razón es un agujero que nadie
  recuerda.

### Arreglado

**Los seis hallazgos del field test que bloqueaban la adopción están cerrados**, cada uno con un
test que falla contra el commit anterior. El triage fue con una sola pregunta: ¿impide que un
adoptante externo llegue al final del flujo, o rompe la promesa central en silencio?

- **#4 — `claim` repartía una cerca que no podía honrar.** Solo miraba dentro de su propia corrida,
  así que la segunda corrida de un proyecto reclamaba la misma ruta, se le decía *"any other agent
  writing them is now denied"*, y después el guard denegaba a **ambos** dueños al momento de
  escribir. La mitad del mecanismo ya existía y nunca se llamaba desde acá:
  `store.WriteSetOwnerAcrossOpenRuns`, la misma query que usa el guard de escritura. El
  descubrimiento se mueve de mitad-de-fan-out, a ambos agentes, a tiempo de claim, al único que
  todavía puede cambiar el plan. Dos worktrees siguen permitidos — ese es el arreglo que los
  propios mensajes de batten recomiendan.
- **#7 — un claim fuera de la raíz del repo se aceptaba con la misma línea de éxito.** El guard
  compara rutas relativas al repo, así que nunca podía matchear: una cerca imaginaria alrededor de
  un archivo que batten jamás va a custodiar. Ahora se rechaza por nombre, y un argumento relativo
  se resuelve contra la raíz.
- **#16 — el flujo documentado terminaba en una denegación que nombraba un comando que los
  documentos no contenían.** Con `checks:` declarado, el gate quiere dos veredictos de dos
  productores, y ningún comando `/batten-*` ni skill mencionaba `batten check`. `/batten-verify`
  decía correr los checks del gate a mano, lo que produce citas y no un pase con origen en batten.
  Un guard sostiene ahora la regla en vez de la lista: todo documento que nombra `batten verdict`
  debe nombrar `batten check`.
- **#27 — evidencia con objetos JSON devolvía la sentencia del propio decoder de Go.** En el
  momento del primer veredicto de un adoptante, `json: cannot unmarshal object into Go struct
  field Verdict.evidence of type string` nombraba un tipo de Go y un campo de struct que no
  aparecen en ninguna parte de la documentación que estaba siguiendo. El error nombra ahora el
  campo, la forma que quiere y la convención que reemplaza al objeto (`AC-<n>:` como prefijo) —
  rechazar sin enseñar manda al agente a adivinar, y vuelve a adivinar objetos.
- **#50 — un commit cerraba el unit con el que arrancó la sesión, no el unit que nombra.** El gate
  había aprendido a leer el mensaje de commit; el camino de cierre no. En trunk, donde la rama no
  nombra nada, `feat(TASK-002)` se gateaba como TASK-002 y cerraba TASK-001 — marcando ok un unit
  que nadie commiteó y liberando sus claims de write-set mientras todavía se estaba trabajando.
  Los dos sitios resuelven ahora el unit por una sola función, porque contestar la misma pregunta
  distinto en dos lugares es lo que produjo el bug.
- **#59 — lo primero que batten le pide a cualquiera commiteaba la propia base de datos de
  batten.** `batten init` escribía `batten.yaml` y no decía nada del `.gitignore`. Ahora agrega
  `/.batten/` cuando falta, apendeando en vez de reescribir — `init` es un comando de primer
  contacto en el repo de otro — y dice que lo hizo.

- **El sujeto privado del field test ya no se nombra fuera de `docs/field-test/`.** batten se
  ejercitó contra un repo privado real, y el nombre de ese repo no se había quedado en el reporte:
  había llegado a `internal/scan`, `cmd/batten`, `internal/hooks`, los dos scripts de matriz, el
  ROADMAP y `docs/FIELD-TEST.md` — 10 archivos trackeados de código vivo, tests, scripts y docs,
  ninguno de los cuales habría sido tocado jamás por una decisión sobre `docs/field-test/`. Donde
  el texto quiere decir la réplica, el sujeto es ahora `replica-ui`, el nombre que los scripts ya
  usaban; donde quiere decir el repo real, se lo describe sin nombrarlo.

### Agregado

- **Un guard contra la misma filtración, en sus dos lecturas.** `TestNoPrivateProjectTokensAreTracked`
  (`internal/install`) y un paso a juego en `ci.yml`. Necesitaba la lista de exclusiones *opuesta*
  a la del guard de rutas personales que tiene al lado, y llegar ahí tomó dos hallazgos: un token
  no es una ruta, así que `:!graphify-out` tenía que irse — y soltarlo todavía no alcanzaba,
  porque `.gitattributes` marca el grafo generado `-diff` y **`git grep -I` saltea los archivos
  `-diff` como binarios**. Un token plantado en `graph.json` pasó caminando frente al guard que ya
  había soltado la exclusión de ruta. El guard corre sin `-I`; se verificó rojo contra el commit
  anterior, rojo con el token plantado en `graph.json`, y verde sobre el árbol barrido.

### Lo que cambió el field test

Es un **beta** por una razón honesta: batten nunca fue instalado en un repositorio distinto de
este, por nadie que no lo haya escrito. Todo lo de abajo fue ejercitado, y la mayor parte se
encontró ejercitándolo.

### El field test, y lo que cambió

Antes de esta versión, batten se corrió contra una réplica de un proyecto real de cuatro dominios.
Eso produjo **80 hallazgos**; los 63 que no se habían arreglado en el momento pasaron por un
verificador adversarial que intentó refutar cada uno, dejando **52 confirmados y 11 refutados**.
Esos 52 se trabajaron después en cuatro bloques. **45 están arreglados y verificados en este tag;
7 siguen abiertos** y están listados bajo *Brechas conocidas* en vez de cargados en silencio. Ese
número ya estuvo mal dos veces, así que ahora viaja con su regla de conteo: un hallazgo está
abierto si está CONFIRMED en `verified.json` y no tiene arreglo en HEAD, contado una vez.

Esa proporción es el titular honesto de este release. La materia prima de los hallazgos — 63
veredictos con sus reproducciones, evidencia verbatim y controles positivos — vivía en
`docs/field-test/` y fue **retirada del árbol antes de este tag**: describía un repositorio
privado y no la réplica sintética, y ninguna cantidad de reescritura vuelve publicable un
documento sobre el codebase de otro. Queda en la historia de git. Lo que sobrevive a la vista es
el análisis: [`docs/FIELD-TEST.es.md`](docs/FIELD-TEST.es.md) ([en](docs/FIELD-TEST.md)), y la
matriz de aceptación reconstruida
como un script que cualquiera puede correr — [`scripts/matrix-replica.sh`](scripts/matrix-replica.sh).

La única cosa más útil que enseñó el field test fue estructural: un tercio de los hallazgos eran
**una falla repetida nueve veces** — batten declarando una capacidad de gobernanza que no imponía.
Esa es precisamente la falla que batten existe para sacar de los flujos ajenos, así que el arreglo
fue un mecanismo y no nueve parches. Cuatro tests guard sostienen ahora la línea:

| guard | qué impone |
|---|---|
| `TestEveryDeclaredFieldHasAConsumerOrIsDeclaredFuture` | cada campo de `batten.yaml` tiene un consumidor en producción, o una entrada explícita con una razón |
| `TestDeclaredAsFutureHasNoStaleEntries` | esa lista no puede documentar promesas que ya no existen |
| `TestEveryUnattendedRuleIsMechanicalOrRegisteredAsProse` | cada regla absoluta de `/batten-night` tiene un mecanismo cuyo identificador se *usa*, no meramente se declara |
| `TestEveryEdgeRelationReadHasAProducer` | cada `edges.rel` que una superficie lee tiene algo que lo escribe |

Agregar un campo y olvidarlo ya no es posible. La lista de campos declarados-pero-no-implementados
pasó de 16 a **7** — cuatro cableados, tres borrados del spec por completo.

### Agregado

- **Cinco reglas de proceso se volvieron denegaciones mecánicas**, sumándose a las dos que ya lo
  eran (el gate del commit y el write-set guard). Mergear un worktree sin los dos veredictos,
  borrar algo durante una corrida desatendida, `batten override` mientras nadie mira, commitear
  durante una corrida desatendida *con* los veredictos puestos, y pasarse del techo de
  iteraciones — todas eran prosa pidiéndole al modelo que se porte bien, en el comando más
  peligroso que envía el plugin.
- **`batten worktree`** — un árbol por work item, creado, registrado y anclado, con el merge de
  vuelta gateado por la misma condición que un commit. batten había recomendado esto en tres
  mensajes distintos y después lo *castigaba*: dos units en dos árboles editando la misma ruta
  relativa parecían dos sesiones peleándose por un archivo, y el guard denegaba a las dos. El lock
  vive en el **git-common-dir**, porque el `.git` de un worktree enlazado es un *archivo* y un
  lock relativo a él es por-worktree — cada proceso toma el suyo y la exclusión mutua es
  imaginaria.
- **`batten unattended` / `batten iterate`** — el techo de iteraciones del loop sin supervisión se
  cuenta y se impone. Estaba declarado en el spec, se devolvía por MCP, se *dibujaba en la TUI*
  como `iters %d/%d`, y nada lo incrementaba jamás: `runs.iterations` fue 0 para siempre. Ninguna
  de las cuatro denegaciones desatendidas lleva campo `fix`, a propósito — la salida es `--off`, e
  imprimirle eso a un loop que nadie mira es entregarle la llave de su propia cerca.
- **`batten status`** — el backlog contra el registro: cada work item que el documento de plan
  define, con su estado de corrida y su cobertura de criterios de aceptación, *incluyendo los que
  nadie arrancó*. Esa es la mitad que `batten runs` no puede mostrar. El trabajo ad-hoc se lista
  aparte para que la vista nunca implique que el backlog es el mundo entero.
- **Criterios de aceptación como dato.** "Criterios" aparecía diez veces en la prosa del codebase
  y cero veces como dato. Un nuevo `internal/plan` lee `unit.plan` + `unit.locator` — que
  `batten init` escribía desde el día uno y nada leía de vuelta — en bloques de work item;
  `batten phase` siembra una tabla `criteria` desde el bloque del item; y un veredicto
  **aprobatorio** cubre exactamente los criterios que su evidencia cita como `AC-<n>:`. Un
  veredicto `blocked` que nombra `AC-2` está describiendo qué falló y no cubre nada. `batten pr`
  ahora dice *"AC-1 covered by X"* con los no cubiertos nombrados en voz alta, y el briefing de la
  fase de gate lista la numeración para que un revisor la use sin preguntar.
- **`batten scan-diff`** — le pregunta a git qué cambió y al ledger quién reclamó qué, y los
  contrasta. Determinístico, sin parseo de shell, sin falsos positivos, así que un generador de
  código, un target de Makefile o un script de `python` es tan visible como un `sed`. Se niega a
  concluir dos cosas que no puede saber: *quién* tocó un archivo sin reclamar, y que una corrida
  con cero claims esté limpia.
- **`batten report`** — lo que batten vio y lo que *detuvo*, con una forma markdown `--share`.
  Cada conteo declara la fecha desde la que cuenta: "2 commits denied" se lee como total histórico
  cuando batten puede estar contando desde el martes.
- **`batten pr`** — el cuerpo del pull request desde el registro: un DAG Mermaid que GitHub
  renderiza nativo, la tabla de verificación con evidencia citada, la cobertura de criterios y el
  costo. Si el uso no se midió dice `NOT MEASURED`, nunca `$0.00`.
- **`batten canvas --html`** — el grafo de la corrida como una sola página autocontenida de
  ~10 KB, sin ningún request de red. Y el export JSON Canvas para Obsidian.
- **`batten demo`** — el flujo entero sobre un repo git descartable en unos 30 segundos, sin tocar
  nada tuyo. La adopción tomaba ~8 pasos para llegar a un commit denegado.
- **`batten recover`** — re-ancla una corrida a la que se le movió la base, y dice *qué* le pasó
  al ancla vieja. "Alguien editó tu archivo" y "la historia se movió debajo tuyo" necesitan
  consejos opuestos, así que el fingerprint del árbol guarda el commit y el árbol por separado.
- **`batten doctor` diagnostica todo en una pasada**, con la corrección al lado de cada problema.
  Antes se detenía en el primer fatal, que es como la gente abandona en la tercera iteración.
- **Un sobre de falla tipado en cada denegación y cada advertencia** — `batten.code` (un
  identificador string estable), `batten.fix` (el comando exacto), `batten.retry` (si re-ejecutar
  la misma llamada podría funcionar). 17 códigos. `retry` es el caro de errar: un veredicto
  faltante *no* es retryable, y un loop que lo reintenta quema la ventana en una denegación
  idéntica.
- **El grafo de corridas ganó aristas tipadas con productores reales**: `retry_of` tenía cinco
  lectores y cero escritores — `batten pr` contaba reintentos para su badge, el canvas pintaba la
  arista de naranja, la nota del vault la listaba, MCP contestaba `retries: N`, la TUI la colgaba
  del nodo — y la fila solo se había insertado a mano, alguna vez. `depends_on` tenía color en el
  canvas y ningún productor: el grafo se decía DAG y no tenía ni una arista entre dos fases.
- **Un guard de escrituras por Bash**, advisory por un ciclo de medición. `Edit` sobre un archivo
  reclamado se denegaba y el `sed -i` byte-equivalente pasaba en silencio, así que el guard sobre
  el que descansa todo el argumento de seguridad del fan-out estaba a un `sed` de ser opcional.
- **La cadena de orientación de tres memorias llega al subagente que escribe código.** No
  consultaba nada y arrancaba leyendo archivos, la más cara de las tres opciones. La instrucción
  la inyecta el binario en el briefing de fase, y *exige* decirlo cuando ninguna de las dos
  memorias contestó — un agente al que se le pide consultar dos herramientas va a reportar
  haberlas consultado de cualquier manera.

### Arreglado — la instalación, que es donde de verdad se juzga un primer release

Cinco de estos eran bloqueantes encontrados auditando la *distribución*, no el motor. batten
funcionaba; una persona que recibía batten, no.

- **El binario se descargaba a una ruta que nada invoca.** `bootstrap.sh` instalaba en
  `${CLAUDE_PLUGIN_DATA}/bin` mientras que los ocho hooks de `hooks.json`, el servidor MCP en
  `.mcp.json`, el `batten` pelado de cada comando `/batten-*` (que resuelve solo porque Claude
  Code pone el `bin/` de un plugin en el PATH) y `batten doctor` nombran todos
  `${CLAUDE_PLUGIN_ROOT}/bin/batten`. Una instalación desde release, por lo tanto, imprimía
  `installed` y después: ningún hook corrió, el servidor MCP nunca arrancó, cada bloque bash era
  `command not found`, y doctor reportaba *"the gate is not running at all"* sobre una máquina
  donde el bootstrap acababa de tener éxito. Un contrato escrito en cuatro archivos, y nada los
  comparaba. `${CLAUDE_PLUGIN_ROOT}/bin` es ahora el destino; `${CLAUDE_PLUGIN_DATA}/bin` es una
  caché que sobrevive a las actualizaciones del plugin, así que una actualización cuesta una copia
  en vez de 14 MB.
- **Y el mismo bug se hacía permanente a sí mismo.** El primer chequeo era `command -v batten`,
  que un build de desarrollo, una copia de `go install` o una entrada rancia del PATH satisfacen
  igual — así que después de que una actualización vaciara `$ROOT/bin`, el bootstrap declaraba
  victoria sobre un directorio vacío para siempre. El chequeo nombra ahora el archivo, porque
  nombrar el PATH *era* el bug.
- **Las dos copias de `bootstrap.sh` estaban commiteadas sin el bit de ejecución** —
  `Permission denied` en macOS y Linux, las dos plataformas que el autor de este repo no puede
  reproducir, con el binario nunca descargado y cada hook no-opeando en silencio. La misma clase
  que el problema de CRLF que `.gitattributes` ya resuelve, y ahora tiene el mismo tratamiento: el
  modo está arreglado, CI rechaza un `.sh` trackeado que no sea `100755`, y `hooks.json` nombra el
  intérprete para que un bit perdido en tránsito degrade a una instalación que funciona en vez de
  a un gate muerto.
- **Windows sin Git Bash no tenía forma de instalar en absoluto**, en la plataforma que este
  proyecto declara primaria. `bootstrap.ps1` (PowerShell 5.1, el que viene en la caja) y
  `bootstrap.cmd` existen ahora, y el hook despacha con `bash … || powershell …` — que es
  inequívoco porque los dos scripts salen con 0 incluso cuando la descarga falla, así que el
  fallback dispara en exactamente una condición: acá no hay bash.
- **`tar` rompía la descarga en Windows**, encontrado corriendo el script nuevo en vez de
  leyéndolo: el `tar` del PATH suele ser el GNU tar de Git Bash, que lee `C:\Users\…` como un
  *host remoto* — "Cannot connect to C: resolve failed" — y no desempaqueta nada. El camino
  PowerShell llama ahora a `System32\tar.exe` por ruta completa; el camino shell dejó de pasarle
  rutas absolutas a tar del todo.
- **Cada comando `/batten-*` ahora se niega a correr sin el binario** en vez de pisar un
  `command not found` y completar la fase sin gate.
- **`batten.schema.json` rechazaba el propio `batten.yaml` de este repo.** `provenance.format` y
  `models.*` se borraron del struct y del schema en un commit y quedaron atrás en `batten.yaml` y
  dos ejemplos, así que un editor validando contra el schema publicado llamaba inválido al archivo
  mientras `batten doctor` lo llamaba perfecto — y el job de schema de CI estaba rojo sobre el
  spec que *es* el producto.
- **Un typo en una clave de nivel superior ya no es silencioso** — esto era la *brecha conocida
  #1*. `enforcment: report` hacía que doctor imprimiera un verde
  `enforcement: enforce — gates block` y saliera con 0. `batten doctor` nombra ahora cada clave
  que batten no lee. La carga las sigue tolerando, porque un spec escrito para un batten más nuevo
  debe funcionar en uno más viejo; lo que cambia es que te enterás. **Esto bajó el conteo de
  abiertos de 15 a 14** — y mirá *Brechas conocidas* por las dos correcciones que siguieron.

### Arreglado

- **`batten measure` sub-reportaba el gasto de tokens por un factor que dependía del tráfico** —
  21.9× en el field test, 107.7× en la re-verificación. Sumaba input+output mientras que
  `runs.tokens_spent`, recomputado desde las *mismas filas*, sumaba los cinco buckets. El
  invariante es ahora un test: `SUM(measure) == SUM(runs.tokens_spent)` sobre las mismas filas.
- **Un modelo sin tarifa publicada imprimía `$0.00`** bajo un encabezado que dice "spend by
  model", byte-idéntico a una fila medida que genuinamente redondea a cero. Son hechos opuestos y
  ya no comparten renderizado.
- **Una corrida tarifada parcialmente presentaba su cifra en dólares como total.** Con el 38 % de
  los tokens de una corrida en un modelo sin tarifa, `$0.39` es un piso. La porción sin tarifar
  viaja ahora *en el registro de la corrida*, así que ninguna superficie puede dejar de verla —
  `budget`, `runs`, `show`, la TUI, MCP, el PR, el canvas y la nota del vault renderizan todos
  `≥$0.39` y nombran la brecha. Cuatro superficies venían formateando ese número cada una por su
  cuenta, que es exactamente cómo llegaron a discrepar.
- **Un override era invisible a lo largo de toda la CLI.** Después de `batten override`, el gate
  del commit pasa a permitir — y `batten show` seguía imprimiendo *"the close gate will deny a
  commit"*, el opuesto literal de la verdad, en el texto contra el que un agente planifica.
  `store.OverrideFor` devuelve la razón y el timestamp, y las cuatro superficies que no
  preguntaban ahora preguntan, en el orden en el que decide el hook: el override primero, porque
  vuelve irrelevante cualquier otra respuesta.
- **Un commit que batten no podía atribuir pasaba en silencio.** Fallar abierto es la decisión
  correcta — a una herramienta que deniega lo que no puede verificar la desinstalan el día uno —
  pero fallar abierto *en silencio* es peor que no tener gate, porque al gate se le cree. Ahora
  dice qué no se está gateando, y nombra el arreglo.
- **Un id de nodo que no llevaba su corrida no era un identificador.** Los ids de fase eran el
  string global `"p-" + fase`, una fila para toda la base de datos: el segundo work item en entrar
  a `build` se llevaba la fila del primero, y el canvas del primero colapsaba a un encabezado
  pelado.
- **`batten check` por sí solo podía cerrar un unit.** El gate necesita dos veredictos de dos
  productores distintos — el de batten, probando que los checks declarados *corrieron*, y el de un
  revisor, juzgando el trabajo contra sus criterios — y tres superficies leían "la última fila",
  que siempre es la de `batten check`. Su salida quedaba pintada encima de la evidencia del
  revisor, y una corrida que nadie había revisado se archivaba como aprobada.
- **"Verificado" ahora significa verificado contra *este* árbol.** `batten check` probaba que los
  checks pasaron y no grababa rastro de *contra qué* pasaron, así que un formateador entre el
  check y el commit dejaba al veredicto afirmando `batten-verified` sobre un estado que ya no
  existía.
- **batten podía invalidar sus propios veredictos escribiendo su propio ledger.** El primer
  fingerprint de árbol hasheaba todo lo que git reportaba, así que *grabar* el veredicto cambiaba
  el árbol del que el veredicto hablaba.
- **La cerca de write-set compara archivos, no nombres de archivo** (ruta + `os.SameFile`), y
  case-foldea donde el filesystem lo hace — si no, un agente cruza la cerca cambiando la mayúscula
  de una letra.
- **Un claim de directorio o glob se rechaza** en vez de aceptarse, reportarse como protección, y
  no cercar nada. El guard matchea rutas exactas, así que `src/**` era una cerca falsa — y una
  cerca falsa es peor que ninguna, porque el plan confía en ella.
- **Abrir una corrida es trabajo de `batten phase` y de nadie más.** `batten check` sobre un unit
  cerrado bifurcaba en silencio una segunda corrida sin ancla y sin fase, exit 0, y `batten show`
  mostraba después solo ese fork vacío.
- **Los ids de work item se validan.** `batten phase FOO-9 build` abría una corrida fantasma con
  exit 0 mientras el mismo comando rechaza de plano una fase que no existe. El patrón se ancla a
  string completo: con `US-\d{3}`, `US-0001` solía colarse por su prefijo `US-000`.
- **`batten show <unit> --run <id>`** resuelve esa corrida. Antes descartaba el flag y su valor,
  imprimía la última corrida y salía con 0 — incluso para un id que no existe.
- **Los conteos de tokens se renderizan a su propia escala.** El brief de sesión mostraba 42,600
  tokens medidos como `0.0M`, un cero aparente, en la única línea que un agente lee antes de
  decidir si hay presupuesto para trabajar. Cinco paquetes llevaban una copia privada de ese
  formateador y un sexto improvisaba un `%.1fM` a mano; ahora hay uno.
- **Los códigos de salida en Windows dejaron de ser basura.** Un proceso que muere anormalmente
  reporta un NTSTATUS negativo, y el valor crudo se renderizaba como su wraparound de 32 bits sin
  signo: `exit 4294963238` en vez de `-4058` — y ese valor se *persistía* en la evidencia del
  veredicto, que `batten show` reproduce para siempre.
- **`batten tui` rechaza un stdout que no es terminal** en vez de emitir 96 bytes de setup de
  terminal, no renderizar nada, y no salir jamás.
- **`batten init --help` imprime el uso** en vez de escribir `batten.yaml` y salir con 0 — era el
  único comando que a la vez escribe un archivo y no tenía brazo default en su switch de flags.
  `--from` ahora exige que el documento exista y lo *graba* como `unit.plan`; antes era un eco
  puro a stdout, produciendo un archivo byte-idéntico lo pasaras o no.
- **La advertencia de corrida estancada se puede despejar.** Su predicado lee `events.run_id`, y
  el journal escribía NULL en esa columna en cada fila, así que la mitad de "no activity" era
  código muerto y una corrida que se estaba trabajando ahora mismo se reportaba estancada solo por
  edad.
- **Las tarjetas ya no se solapan en el canvas.** Una tarjeta de fase abarca 120 px y su primer
  subagente arrancaba en 60 — media tarjeta de solape, en la superficie que existe para ser
  mirada.
- **`enforcement: report` se graba en cada decisión.** Sin eso, "corrimos en modo report tres
  semanas" no tiene registro de lo que costó — que es la otra mitad de un kill switch que valga la
  pena tener.
- **La contención de SQLite está clasificada.** Un `SQLITE_BUSY` es transitorio y lo dice en el
  sobre; tratarlo como fatal brickearía una sesión, y tratar un veredicto faltante como retryable
  quema la ventana. La identidad es por device+inode, no por nombre.
- **Un tag ya no puede publicar sobre una suite roja.** `release.yml` iba directo a compilar
  binarios; un job `verify` corre ahora la suite completa en las tres plataformas primero, y
  chequea que los manifiestos del plugin concuerden con el tag.

### Quitado

- **`models.tiers`, `models.phases` y `provenance.format` se fueron del spec.** El schema afirmaba
  *"batten routes subagents and verifies it from the ledger"* y batten deliberadamente no
  orquesta, así que esa promesa nunca podía cumplirse; `provenance.format` no tenía ni escritor ni
  lector. El ruteo por dominio sobrevive como `domains.<name>.model`, que batten *sí* verifica
  contra el ledger de uso — `batten show` marca "declared haiku, ran opus" como desviación en vez
  de un sobregasto silencioso. Un campo que un usuario escribe creyendo que gobierna, y que no
  gobierna, es peor que su ausencia.

### Brechas conocidas

**7 de los 52 hallazgos confirmados siguen abiertos en este tag.** Están listados acá en vez de
cargados en silencio, porque un release que esconde su propia lista de defectos es el artefacto
contra el que este proyecto existe para argumentar.

Las reproducciones por hallazgo estaban en `docs/field-test/verified.json`, retirado del árbol
antes de este tag porque describía un repositorio privado (ver arriba). Están en la historia de
git; lo que sigue es la lista misma, que es la parte que te debía una respuesta pública.

**La regla de conteo, porque este número ya estuvo mal dos veces.** Un hallazgo está abierto si
está CONFIRMED en `verified.json` y no tiene arreglo en HEAD, contado **una vez**. La aritmética,
completa:

- **15** era el conteo cuando cerraron los cuatro bloques.
- **14** después de arreglar el hallazgo #1 — una clave de nivel superior ignorada en silencio.
  Nunca había sido triageado a ningún bloque, lo que fue su propia lección: la lista de brechas
  era adonde iba a ser recordado en vez de arreglado.
- **13** después de una corrección de renumeración. Esta lista solía citar *"#6, #60"* para el
  bypass de heredoc/Makefile, como si fueran un hallazgo presentado dos veces. No son el mismo
  hallazgo: **el índice 60 es doctor deteniéndose en el primer error fatal**, y ya estaba cerrado,
  con un test que lo cita por número (`TestDoctorReportsEverythingInOnePass`). El bypass de
  heredoc tampoco es un segundo hallazgo — es la **frontera** declarada del #6. Así que se contó
  un número que no debía contarse, y estaba pegado al defecto equivocado.
- **7** ahora: los seis que bloqueaban a un adoptante externo están arreglados arriba (#4, #7,
  #16, #27, #50, #59).

Lo que queda son seis hallazgos cosméticos y un límite declarado:

- **El write-set guarda y reporta la ruta case-foldeada** (#43), así que `useTrace.ts` vuelve como
  `usetrace.ts` en cada superficie. La cerca en sí es correcta — compara archivos, no nombres —
  pero lo que imprime no es lo que escribiste.
- **`batten runs` no imprime id de corrida, hora de inicio ni edad** (#23), y `show --run <id>`
  descarta el flag en vez de rechazar un id que no existe.
- **Cada superficie muestra solo el ÚLTIMO veredicto** (#28), así que un segundo productor esconde
  al primero de la vista. La base de datos guarda los dos.
- **`measure` imprime un encabezado de headroom en un repo que nunca declaró compresión** (#34).
- **La TUI rotula la misma cantidad `113% quota` en la lista y `17.0%` en el detalle** (#47).
- **`doctor` no menciona una fase que diffea desde un ancla faltante** (#24), aunque el runtime
  avisa de eso.
- **Una escritura que cruza la cerca por un heredoc de `python`, un target de Makefile o una
  herramienta de terceros es invisible para el guard de Bash** (#6). Este es un **límite
  declarado, no un descuido**: ningún parser de shell llega adentro de un heredoc, y poner
  heurísticas más profundas en el camino crítico es lo que el ciclo del bash-guard le enseñó a
  este proyecto a no hacer sin medición. El complemento estructural ya existe y a git no lo engaña
  un heredoc — `batten scan-diff`, ahora cableado en los `gates.checks` de este mismo repo.

Más allá del field test:

- **El plugin nunca se instaló desde un release publicado**, porque no hubo ninguno. La auditoría
  que encontró los tres bloqueantes de instalación de arriba está cerrada, y el camino ahora se
  ejecuta en vez de leerse: `scripts/release-check.sh` compila cruzado las seis plataformas y
  chequea el nombre y los bytes mágicos de cada archivo, y la suite maneja los `bootstrap.sh` y
  `bootstrap.ps1` reales contra un archivo real sobre un servidor local. Lo que ningún chequeo
  local puede probar es que `releases/latest/download` resuelva — para eso hace falta el tag.
- **Los bootstraps verifican un checksum pero ninguna firma.** La mitad sha256 está cerrada (ver
  *Sin publicar*): los dos scripts leen ahora el `checksums.txt` que GoReleaser ya publicaba y se
  niegan a instalar lo que no lo matchea. Lo que un checksum no puede cubrir es una cuenta de
  release comprometida — la misma mano que reemplaza el asset reemplaza su línea en el archivo de
  sumas. Eso necesita minisign con una clave guardada localmente, y es trabajo del 0.1.0, no de
  este tag.
- **El formato de transcript que batten parsea no es una API pública.** Cuando se rompe, batten
  reporta el conteo como no disponible en vez de adivinar — correcto, pero el ledger puede
  quedarse ciego sin aviso.
- **No hay GIF en el README.** Los scripts `.tape` están escritos y verificados en contenido;
  grabarlos necesita vhs + ttyd + ffmpeg.

---

## Antes en 0.1.0-beta.1 — el endurecimiento que precedió al field test

Todo lo de abajo también sale en este tag. Se conserva como su propia tanda de secciones porque se
escribió a medida que aterrizaba, antes de que el field test reencuadrara qué importaba; plegarlo
en las listas de arriba habría significado reescribir entradas que eran precisas cuando se
hicieron.

### Agregado
- **La carpeta del vault se explica sola.** Una nota índice `<project>.md` vive ahora junto a los
  tableros, enlazando cada uno y declarando las dos cosas que un lector tiene que saber antes de
  actuar sobre los números: los dólares imputados **no son una factura** (en una suscripción
  ningún token tiene costo marginal), y cada nota es una proyección — SQLite es canónico.
- **`batten doctor` reporta la integración con git de graphify.** `graph.json` se commitea a
  propósito como artefacto compartido y es un megabyte de JSON generado, así que dos ramas que
  tocan código conflictúan en él inevitablemente. `graphify hook install` registra un merge driver
  union exactamente para eso, y doctor ahora lo dice cuando el grafo está trackeado sin él.
- **`/batten-plan` le pregunta al grafo por `god-nodes --json` y `affected`** en vez de leer
  `GRAPH_REPORT.md`, cuya redacción está escrita para humanos y cambia entre versiones. `affected`
  es el más filoso de los dos: un radio de impacto que cruza un dominio que el plan no contempló
  significa que los write-sets no son realmente disjuntos — mejor encontrarlo en tiempo de plan
  que en tiempo de merge.
- Tests para cada paquete salvo `internal/tui` (un visor de solo lectura): `internal/spec` 94.9%,
  `internal/vault` 92.1%, `internal/canvas` 86%, `internal/export` 84.8%, `internal/store` 62.5%,
  `internal/hooks` 29.5%. Total 52.7%, desde 0% en seis de ellos.

### Arreglado
- **Los ids de corrida colisionaban en Windows.** `EnsureRun` armaba su id desde
  `time.Now().UnixNano()`, que es único solo si dos llamadas nunca caen en el mismo tick — y la
  granularidad del reloj de Windows suele ser medio milisegundo o peor. Cerrar una corrida y abrir
  la siguiente para el mismo unit producía el mismo nanosegundo y fallaba en la primary key, desde
  el camino del hook, donde un error es una sesión rota. Cuatro bytes aleatorios hacen ahora el
  trabajo que se le suponía al timestamp.
- **"La última corrida" era una moneda al aire.** `started_at` se guarda en segundos, así que dos
  corridas de un unit abiertas en el mismo segundo dejaban a `ORDER BY started_at DESC LIMIT 1`
  libre de devolver cualquiera, y `batten show` podía inspeccionar la más vieja. Un desempate por
  rowid lo vuelve determinístico.
- **`batten doctor` sugería un comando de graphify que no existe.** `graphify . --update` no es un
  flag; graphify lo ignora, corre una extracción completa y falla por una API key de LLM faltante.
  El comando es `graphify update .`. Es la segunda pista de acá en sobrevivir a la CLI de
  graphify.
- Dos rutas absolutas personales seguían trackeadas en los docs. CI ahora falla ante cualquier
  ruta home absoluta trackeada o archivo de trabajo privado.
- **CI, que no existía.** `release.yml` disparaba con un tag e iba directo a compilar binarios,
  así que un tag podía publicar sobre una suite roja. Ahora hay un `ci.yml` en cada push y pull
  request, y el release no arranca GoReleaser hasta que pasa. La matriz es Linux, macOS y Windows
  porque batten bifurca por `runtime.GOOS` en tres lugares que deciden si un guard sostiene —
  sobre todo `store.normPath`, que case-foldea donde el filesystem es case-insensitive para que un
  agente no pueda cruzar la cerca del write-set cambiando la mayúscula de una letra. CI además
  fija los contratos que ya habían derivado: la copia generada de `bootstrap.sh` del plugin
  coincidiendo con su fuente, el nombre de descarga coincidiendo con el template del archivo, cada
  ejemplo validando contra el schema *y* cargando en el parser real, más `gofmt` y `go mod tidy`.
- **`LICENSE`.** El README, el manifiesto del plugin y el archivo de release afirmaban todos MIT;
  sin el texto, el default es todos-los-derechos-reservados.
- **`.gitattributes`.** Un `bootstrap.sh` commiteado con CRLF tiene un shebang de
  `#!/usr/bin/env bash\r`, que falla en toda máquina no-Windows con "bad interpreter" — binario
  nunca descargado, hooks no-opeando en silencio. Funcionaba solo porque una máquina casualmente
  tenía `core.autocrlf=true`. Ahora decide el repo, no la config de cada desarrollador.
- Tests para `internal/spec` (0% → 94.9%), `internal/canvas` (0% → 86%), `internal/hooks` e
  `internal/scan`, que no tenían ninguno.

### Cambiado
- **El repo es publicable.** Todo apunta a `ArthurZizumbo/batten` — el module path de Go y cada
  import, los dos scripts de bootstrap, el manifiesto del plugin, la entrada del marketplace, el
  `$id` del schema, los docs de instalación. El module path es el caro de cambiar después de
  publicar.

### Arreglado
- **Tres cosas que habrían roto el primer release**, encontradas leyendo el camino de release de
  punta a punta en vez de corriéndolo. GoReleaser compilaba `batten_0.1.0_linux_amd64.tar.gz`
  mientras `bootstrap.sh` buscaba `batten_linux_amd64.tar.gz` desde `releases/latest/download` —
  cada plataforma habría dado 404, ya que ese endpoint solo puede resolver un nombre predecible
  sin el tag. Windows recibía un `.zip` mientras bootstrap corría `tar -xzf`, y bootstrap corre
  bajo Git Bash, cuyo tar no lee zip. Y `bootstrap.sh` imprimía "installed" hubiera tenido éxito o
  no el move; ahora verifica cada paso, corre el binario una vez, y ante una falla remueve el
  archivo instalado a medias y dice llanamente que nada está siendo gateado.
- `docs/INSTALL.md` decía que el estado vive en `${CLAUDE_PLUGIN_DATA}/batten.db` — la divergencia
  exacta que causó dos bugs E0, ya que los procesos de hook tienen esa variable y la terminal del
  usuario no, partiendo el estado en dos bases de datos. Es `~/.batten`, siempre.
- **`batten init` lee el proceso que un repo ya tiene.** El escaneo reporta ahora `harness[]`
  (reglas de agente por directorio, `CONTRIBUTING.md`, otros harnesses de editor, archivos de
  build, docs de flujo en prosa, un spec existente), `stack[]` (lenguajes y tooling desde archivos
  marcadores que existen — los package managers desde el lockfile, jamás inferidos de nombres de
  directorio), y `purpose[]` (dónde el repo dice para qué es). `/batten-init` ganó la entrevista
  que siempre afirmó tener: leer eso primero, y después preguntar por el propósito, los ejes
  reales de fan-out, el patrón del tracker, los comandos que deben pasar, y qué es lo bastante
  escaso como para forzar serialización.
  - Un directorio con un `AGENTS.md` es ahora un dominio incluso sin código debajo. Un repo
    planificado pero no construido devolvía antes una lista de dominios **vacía**, que es al
    revés: esos son exactamente los repos donde los ejes ya están decididos y nada más los revela.
  - Las notas afirmaban que los checks "were taken from your build files" incluso cuando no se
    encontró ninguno y el gate quedó vacío. Un gate vacío no verifica nada, y ahora lo dice.
  - Primeros tests para `internal/scan`, que no tenía ninguno.
- **El grafo de código como capacidad de primera clase.** graphify 0.9.25 cableado — 1043 nodos,
  2100 aristas, 65 comunidades. Cada corrida se estampa al abrir con si existía un grafo fresco, y
  `batten measure` compara corridas con y contra sin, negándose a concluir nada por debajo de 3
  corridas por lado. Los god nodes de `GRAPH_REPORT.md` suben el tier de dificultad de la fase de
  plan: tocar una abstracción central nunca se clasifica como trabajo mecánico.
- **Ruteo de modelos por dificultad** — `models.tiers` / `models.phases` y `model` por dominio.
  `batten show` marca dónde el modelo declarado divergió del realmente usado, leído del ledger y
  no de la intención.
- **`batten check`** corre los checks de un gate de verdad y graba el veredicto con
  `source: batten`. El gate del commit exige *ese* veredicto, lo que mata "escribió que pasa sin
  correrlo".
- **Ciclo de vida de la corrida** — `batten close`, auto-cierre cuando un commit aterriza en la
  fase de cierre, y una advertencia de doctor para corridas dejadas corriendo más de 48 h (una
  corrida estancada mantiene vivos sus claims de write-set).
- **Multi-sesión** — vínculo sesión↔corrida, defensa de write-set *entre* corridas abiertas, y
  ambigüedad visible cuando una sesión no puede saber en qué unit está.
- **Export a vault de Obsidian**, automático en el hook `Stop` y después de guardarse un
  veredicto: una nota por corrida con frontmatter, tableros `.base`, y un grafo de corrida JSON
  Canvas 1.0 embebido.
- **`batten init`** — una entrevista real al repo (`internal/scan`) reemplazando el stub, más
  `enforcement: report` para que los gates puedan adoptarse sin bloquear un sprint activo.
- **Distribución de binarios** — GoReleaser en el tag, y un `bootstrap.sh` que baja el binario de
  la plataforma a `${CLAUDE_PLUGIN_DATA}` en la primera corrida.

### Arreglado
- **[dogfood]** La nota de corrida del vault reportaba `not recorded` para cada write-set y nunca
  imprimía "Files touched". El renderizado era correcto; nada lo poblaba jamás — `WriteSets` se
  seteaba solo en un test. Se agregó `store.WriteSetsByRun` y se cableó en el único camino de
  producción.
- Tres tests de `internal/mcp` estaban rojos por una razón ajena a lo que afirman: la cerca
  temporal descarta uso anterior al `started_at` de su corrida, y el fixture estampaba su fila en
  el epoch, así que cada fila sembrada se descartaba en silencio. La cerca está bien; el fixture
  es anterior a ella.
- **[dogfood]** La atribución de uso está cercada a la vida de la corrida — tokens gastados antes
  de que una corrida abriera pertenecen a la sesión, no a esa corrida. El export de canvas ahora
  también funciona para corridas cerradas.
- **[dogfood]** El export al vault funcionaba solo para corridas abiertas, así que un unit cerrado
  nunca llegaba al vault.
- **[dogfood]** `batten show` inspeccionaba solo corridas activas y quedaba ciego en el momento en
  que un unit cerraba.
- **[dogfood]** Una base de datos para todos los procesos: `CLAUDE_PLUGIN_DATA` se sacó de
  `dbPath`. El estado vive siempre en `~/.batten`. Dos bugs separados vinieron de esa divergencia.
- **[dogfood]** `bootstrap.sh` estaba declarado en `hooks.json` pero nada lo copiaba al paquete
  del plugin, así que nunca estaba ahí para correr.
- Las rutas de write-set se case-foldean también en macOS. Los dos filesystems por defecto son
  case-insensitive, y un guard que tratara `ml/F.py` y `ml/f.py` como archivos distintos dejaría a
  un agente cruzar la cerca cambiando la capitalización.
- Las escrituras sin atribuir advierten en vez de brickear la sesión; los payloads del log de
  eventos están capeados.
- Una cerca de pánico alrededor del comando del hook — un crash en batten no debe llevarse jamás
  una sesión consigo.

### Cambiado
- **El repo deja de cargar lo que no debe publicar.** Una biblioteca privada de prompts, una nota
  personal, un PDF de terceros y un canvas suelto se purgaron de la historia entera. Un
  `bin/batten.exe~` de 14.5 MB — un remanente de hot-swap de Windows que se coló entre reglas de
  ignore que nombraban solo `batten` y `batten.exe` — se destrackeó, y `bin/` se ignora ahora
  entero. Los sidecars locales-de-máquina de graphify se removieron del índice, que la limpieza
  anterior había agregado a `.gitignore` sin destrackear.

(La lista de *Brechas conocidas* que solía cerrar esta sección fue superada por la de
[0.1.0-beta.1](#0.1.0-beta.1--2026-07-29) arriba, que está medida en vez de resumida.)
