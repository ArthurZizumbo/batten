# Instalar batten en un proyecto

batten se instala como plugin de Claude Code, y **el binario llega solo**: un hook `SessionStart`
corre el bootstrap, que en el primer arranque descarga el binario estático de tu plataforma desde
el GitHub Release a **`${CLAUDE_PLUGIN_ROOT}/bin/batten`**. Esa ruta no es una preferencia: es la
única que los ocho hooks y el servidor MCP nombran, y es el directorio que Claude Code agrega al
PATH — que es lo que hace que el `batten` pelado de los comandos `/batten-*` resuelva.

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
