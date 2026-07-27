# E0 — spike de validación (lo que solo Arthur puede correr)

batten no puede probarse a sí mismo instalado: Claude Code no carga plugins ni dispara hooks
reales desde dentro de una sesión. Estos 5 pasos los corre Arthur, una vez, y anotan la
evidencia que decide el diseño final de la distribución (P3) y los hooks.

## Preparación (una vez)

```powershell
# desde el repo:
powershell -File scripts/build-plugin.ps1
```
Deja `plugin/claude-code/bin/batten.exe` fresco. Luego, en Claude Code:
```
/plugin marketplace add <ruta-absoluta-a-este-repo>
/plugin install batten@batten
```

## 1. ¿Windows resuelve el binario en hooks exec-form?

Tras instalar, abre una sesión en este repo y corre cualquier comando (`ls`). Si el status line
o `batten doctor` responde, el hook arrancó. Si NO:
- editar `plugin/claude-code/hooks/hooks.json`: cambiar `"${CLAUDE_PLUGIN_ROOT}/bin/batten"` por
  `"${CLAUDE_PLUGIN_ROOT}/bin/batten.exe"`, reinstalar, reprobar.
- si aún no: pasar a shell-form (estilo engram, corre bajo Git Bash). Anotar cuál funcionó.

**Resultado:** ✓ (2026-07-23) El exec-form `${CLAUDE_PLUGIN_ROOT}/bin/batten` SIN `.exe` resolvió en Windows 11 — evento SessionStart en la DB del plugin al primer arranque. No hubo que tocar hooks.json.

## 2. ¿`PreToolUse` dentro de un subagente trae `agent_id`?  (LA incógnita crítica)

El write-set guard entero cuelga de este campo. Para capturarlo sin adivinar, hay un logger:

```
batten hook-debug --tap
```
Esto instala temporalmente un hook que vuelca cada payload de `PreToolUse` a
`${CLAUDE_PLUGIN_DATA}/hook-taps.jsonl`. Entonces:
1. Lanza un subagente que edite un archivo (cualquier Task que haga un Write/Edit).
2. Inspecciona el tap: `batten hook-debug --show`
3. Busca si las líneas con `"tool_name":"Edit"` **dentro del subagente** traen `"agent_id"`.

- Si SÍ trae `agent_id` → el guard funciona como está. ✓
- Si NO → el guard cae a modo advisory automáticamente (ya implementado en P2.5): en vez de
  `deny` emite `additionalContext` con la advertencia de colisión. Documentar la limitación.

**Resultado:** ✓ (2026-07-23) SÍ trae `agent_id`. Capturado real: Edit/Write dentro de un subagente llegan con `agent_id` (coincide con el id que devuelve la Agent tool), `agent_type`, `session_id`, `tool_use_id`. SubagentStart/Stop también disparan. El write-set guard opera en modo deny completo. (Fix de paso: el tap ahora vive SIEMPRE en ~/.batten — antes el flag y el hook divergían según CLAUDE_PLUGIN_DATA y el tap capturaba nada.)

## 3. ¿El servidor MCP responde a un cliente real?

Tras instalar, en la sesión: las tools `batten_runs`, `batten_verdict_status`, `batten_spec`,
etc. deben aparecer. Pídele al agente que llame `batten_verdict_status` para una unidad.

**Resultado:** ✓ (2026-07-23) `/mcp` lista el server batten con las 6 tools; los slash commands `/batten:*` autocompletan; los skills aparecen en la sesión.

## 4. TUI interactiva

```
batten tui
```
Debe pintar la lista de runs + panel de detalle. `j/k` mueve, `r` refresca, `q` sale.

**Resultado:** ✓ (2026-07-23) TUI pinta lista + detalle con datos reales: TASK-1 running/build, anchor, barra de tokens 0/5.0M, y el panel rojo NO VERDICT / commit will be DENIED. j/k/r/q funcionan.

## 5. statusline en tu terminal real

```
batten statusline --install
```
Reinicia la sesión. El status line debe mostrar la unidad activa + cuota (si eres Max/Pro).

**Resultado:** ✓ (2026-07-23) `statusline --install` escribió .claude/settings.json y reporta que muestrea la cuota. (Hallazgo aparte del arranque: hooks.json declaraba scripts/bootstrap.sh que no viajaba dentro del paquete — cada SessionStart imprimía 'No such file or directory'. Corregido: el build lo copia adentro.)

---

Cuando tengas los 5 resultados, pégalos aquí o en el chat. El único que puede cambiar diseño
es el #2 (agent_id) y el #1 (forma del hook en Windows) — el resto son confirmaciones.
