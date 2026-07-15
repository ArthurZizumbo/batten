# Instalar batten en un proyecto

batten se instala como plugin de Claude Code. El binario Go va dentro del plugin (Claude Code
añade `bin/` de un plugin al PATH automáticamente), así que los hooks lo encuentran sin pasos
fuera de banda.

## Instalación (5 pasos)

```
# 1. registrar el marketplace (una vez por máquina)
#    - desde un release publicado:
/plugin marketplace add arthu/batten
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

`${CLAUDE_PLUGIN_DATA}/batten.db` (sobrevive a las actualizaciones del plugin;
`${CLAUDE_PLUGIN_ROOT}` NO — se borra en cada update, por eso el estado nunca vive ahí).
Override con `BATTEN_DB`.

## Desinstalar

`/plugin uninstall batten@batten`. La DB queda salvo que borres `${CLAUDE_PLUGIN_DATA}`.
