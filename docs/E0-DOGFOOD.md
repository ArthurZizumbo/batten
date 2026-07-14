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
/plugin marketplace add C:/Users/arthu/Proyectos/Public/LoopWorkFlow
/plugin install batten@batten
```

## 1. ¿Windows resuelve el binario en hooks exec-form?

Tras instalar, abre una sesión en este repo y corre cualquier comando (`ls`). Si el status line
o `batten doctor` responde, el hook arrancó. Si NO:
- editar `plugin/claude-code/hooks/hooks.json`: cambiar `"${CLAUDE_PLUGIN_ROOT}/bin/batten"` por
  `"${CLAUDE_PLUGIN_ROOT}/bin/batten.exe"`, reinstalar, reprobar.
- si aún no: pasar a shell-form (estilo engram, corre bajo Git Bash). Anotar cuál funcionó.

**Resultado:** _______________

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

**Resultado:** _______________

## 3. ¿El servidor MCP responde a un cliente real?

Tras instalar, en la sesión: las tools `batten_runs`, `batten_verdict_status`, `batten_spec`,
etc. deben aparecer. Pídele al agente que llame `batten_verdict_status` para una unidad.

**Resultado:** _______________

## 4. TUI interactiva

```
batten tui
```
Debe pintar la lista de runs + panel de detalle. `j/k` mueve, `r` refresca, `q` sale.

**Resultado:** _______________

## 5. statusline en tu terminal real

```
batten statusline --install
```
Reinicia la sesión. El status line debe mostrar la unidad activa + cuota (si eres Max/Pro).

**Resultado:** _______________

---

Cuando tengas los 5 resultados, pégalos aquí o en el chat. El único que puede cambiar diseño
es el #2 (agent_id) y el #1 (forma del hook en Windows) — el resto son confirmaciones.
