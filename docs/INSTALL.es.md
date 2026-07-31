# Instalar batten en un proyecto

> [English](INSTALL.md) · **Español**

batten se instala como plugin de Claude Code, y **el binario llega solo**: un hook `SessionStart`
corre el bootstrap, que en el primer arranque descarga el binario estático de tu plataforma desde
el GitHub Release a **`${CLAUDE_PLUGIN_ROOT}/bin/batten`**. Esa ruta no es una preferencia: es la
única que los hooks y el servidor MCP nombran, y es el directorio que Claude Code agrega al PATH —
que es lo que hace que el `batten` pelado de los comandos `/batten-*` resuelva.

> *(Regla de conteo, porque este repo publicó "7 hooks" y "8 hooks" a la vez: `hooks.json` declara
> **8 entradas** sobre **6 eventos**. Siete invocan el binario; la octava es el bootstrap, que es
> forma shell porque tiene que correr cuando el binario todavía no existe.)*

**Qué verifica antes de instalar nada.** El bootstrap baja también el `checksums.txt` del mismo
release, saca **la línea de su propio asset** y compara. Es la única parte del bootstrap que
**falla cerrado**: hash distinto, `checksums.txt` inalcanzable, o una máquina sin herramienta de
sha256 son la misma frase —nadie puede responder por estos bytes— y reciben la misma respuesta: no
se instala nada, el caché no se siembra, y stderr nombra la url, el hash esperado y el obtenido.

Se guarda además una copia en `${CLAUDE_PLUGIN_DATA}/bin`. Eso es **caché**: `${CLAUDE_PLUGIN_ROOT}`
se borra en cada actualización del plugin, así que después de un update el bootstrap restaura desde
la copia en vez de volver a bajar 14 MB.

Si compilaste en local, el binario ya está en el `bin/` del plugin y bootstrap no descarga nada.

Si la descarga falla, lo dice y los hooks no-opean: **nada queda gobernado**, y batten prefiere
avisarlo a fingir que te está protegiendo.

### Windows sin Git Bash

El hook intenta `bash bootstrap.sh` y, si esta máquina no tiene bash, cae a
`bootstrap.ps1` — PowerShell 5.1, el que viene en la caja. No hay que instalar nada. Para correrlo
a mano (o si querés ver la salida completa):

```
<ruta-del-plugin>\scripts\bootstrap.cmd
```

Requiere `System32\tar.exe`, que Windows trae desde 10 1803. El script lo invoca por ruta completa
a propósito: el `tar` del PATH suele ser el GNU tar de Git Bash, que lee `C:\Users\...` como un host
remoto y no desempaca nada.

### Windows y el antivirus

**Es esperable que Defender se queje al menos una vez, y no es que batten esté infectado.**
Windows Defender clasifica binarios de Go recién compilados y **sin firmar** como
`Trojan:Win32/*!ml`. El sufijo `!ml` es un veredicto de un modelo de machine learning, no una
firma, y se comporta como tal: le pasó al binario de este proyecto, con dos builds del **mismo**
código dando respuestas distintas y un re-escaneo explícito de esos mismos bytes volviendo limpio.
Eso es la forma de un falso positivo. batten todavía no está firmado con Authenticode.

Importa más que un cartel feo. Si el binario cae en cuarentena **después** de instalarse, cada
hook queda apuntando a un archivo que ya no existe, mueren en silencio, y `batten doctor` no puede
avisarte porque doctor **es** el binario que falta. El bootstrap detecta el patrón —una segunda
restauración desde el caché dentro del mismo día— y lo dice en `SessionStart`. Si ves ese mensaje,
mirá la cuarentena de tu antivirus.

Qué podés hacer:

- **Reportar el falso positivo** en <https://www.microsoft.com/en-us/wdsi/filesubmission>. Es
  gratis y sirve para ese binario; cada release es uno nuevo.
- **Compilarlo vos** — ver abajo. Un binario que produce tu propio toolchain no viaja por la red.

## Compilarlo vos, sin descargar nada

Tres caminos, y ninguno reemplaza al bootstrap: los tres terminan en el mismo lugar, porque
`${CLAUDE_PLUGIN_ROOT}/bin/batten` es la única ruta que los hooks invocan.

```bash
# a) desde un checkout — el camino de desarrollo, deja el binario donde va
scripts/build-plugin.sh          # macOS/Linux
scripts/build-plugin.ps1         # Windows

# b) go install, sin clonar nada
go install github.com/ArthurZizumbo/batten/cmd/batten@latest
#    deja el binario en $(go env GOPATH)/bin. Eso NO alcanza por sí solo: hay que ponerlo
#    donde los hooks miran.
cp "$(go env GOPATH)/bin/batten" "$CLAUDE_PLUGIN_ROOT/bin/batten"       # .exe en Windows

# c) desde el checkout, a mano
go build -o "$CLAUDE_PLUGIN_ROOT/bin/batten" ./cmd/batten
```

Con el binario ya en su lugar, el bootstrap lo ve y **no descarga nada** — es un `stat` y sale.

> **Por qué el paso de copiar no se puede saltear**, aunque engram sí lo permita: el `.mcp.json` de
> engram invoca `engram` pelado y resuelve por PATH. batten deliberadamente no hace eso, y la razón
> tiene nombre — un `batten` cualquiera en el PATH satisfacía `command -v batten` mientras el
> archivo que los hooks nombran no existía, así que el bootstrap cantaba victoria sobre un `bin/`
> vacío y nada quedaba gobernado. Los hooks nombran un archivo, no un comando.

> **Un detalle honesto de `v0.1.0-beta.1`:** un binario hecho con `go install` reporta
> `batten 0.1.0` en vez de `0.1.0-beta.1`, porque la versión la inyecta GoReleaser por ldflags y
> ese tag todavía no traía el fallback que la lee del módulo. `batten doctor` va a decir que el
> plugin y el binario no coinciden. Está arreglado para el próximo tag; mientras tanto, usá (a) o
> (c), o ignorá ese aviso puntual.

## Instalación (5 pasos)

```
# 1. registrar el marketplace (una vez por máquina)
#    - desde un release publicado:
/plugin marketplace add ArthurZizumbo/batten
#    - o desde un checkout local (dev): primero compila el binario, luego:
scripts/build-plugin.ps1            # Windows
scripts/build-plugin.sh             # macOS/Linux
/plugin marketplace add <ruta-al-repo>

# 2. instalar
/plugin install batten@batten

# 3. generar el spec entrevistando el repo
/batten-init                        # o en terminal: batten init

# 4. (opcional) habilitar el techo de cuota de suscripción
batten statusline --install

# 5. verificar
batten doctor                       # verde -> listo
```

## Adoptar en un proyecto que YA está en desarrollo

Este es el caso normal, y batten está hecho para no estorbar:

- **`batten init` arranca en `enforcement: report`.** Los gates ADVIERTEN, no bloquean. Puedes
  adoptar batten a mitad de un sprint sin que nadie choque contra un `deny` el día uno.
- Cuando el equipo confía en los gates, cambia una línea del `batten.yaml`:
  `enforcement: enforce` (o borra la línea; enforce es el default).
- **Ramas ya abiertas**: si tu rama nombra la unidad (`feature/US-034-...`), batten la liga sola.
  Si no, corre `batten phase <unit> <fase>` una vez para ligar esta sesión a su unidad.
- **Migrar tu flujo en prosa**: si ya tienes el proceso escrito (un `prompts.md`, un
  `CONTRIBUTING.md`), pásalo: `/batten-init --from docs/tu-flujo.md`. El agente reconcilia tu
  prosa contra el borrador que el escaneo generó.

## Dos o más sesiones en paralelo

batten no se rompe con dos Claude Code trabajando el mismo repo:

- Cada sesión queda ligada a **su** unidad (por sesión, no por rama compartida).
- Si la sesión B intenta escribir un archivo que un agente de la sesión A reclamó, batten lo
  detiene nombrando la unidad en conflicto.
- Si una sesión no está ligada a ninguna unidad (ambiguo), el `SessionStart` lo dice — y avisa
  que los gates no pueden actuar hasta que la ligues.
- **Recomendado para trabajo paralelo pesado**: un git worktree por unidad. Cada worktree tiene
  su rama → la atribución por rama vuelve a ser automática, y el ledger + el guard entre runs
  se comparten porque la DB de batten es global a la máquina.

## Dónde vive el estado

**`~/.batten/batten.db`**, siempre. Override con `BATTEN_DB`.

Esa ruta es deliberada y vale la pena explicarla, porque costó dos bugs en el dogfood. El estado
NO vive en `${CLAUDE_PLUGIN_DATA}`: los procesos de hook tienen esa variable, pero tu terminal no,
así que una ruta que dependa del entorno parte el estado en dos bases de datos — la TUI dice "no
hay runs" mientras los hooks escriben runs felizmente en otro lado. Y `${CLAUDE_PLUGIN_ROOT}`
queda prohibido en cualquier caso: se borra en cada actualización del plugin.

El binario vive en `${CLAUDE_PLUGIN_ROOT}/bin` porque es el único lugar donde los hooks lo
invocan, y su copia en `${CLAUDE_PLUGIN_DATA}/bin` es caché, no estado: perderla solo cuesta una
descarga.

## Desinstalar

`/plugin uninstall batten@batten`. La DB sobrevive: está en `~/.batten`, fuera del plugin. Bórrala
a mano si de verdad quieres empezar de cero.
