#!/usr/bin/env bash
# matrix-replica.sh — la matriz de aceptación sobre la réplica `replica-ui`.
#
# POR QUÉ ESTO ES UN SCRIPT COMMITEADO Y NO UNA LISTA EN UN DOCUMENTO
#
# La matriz existía como prosa en un documento con ocho pruebas enumeradas, y los
# conteos que se reportaban al cerrar cada bloque (11/11, después 12/12) no correspondían a ninguna
# lista escrita: las pruebas nuevas vivían en la memoria de quien las había corrido. Una matriz de
# aceptación que nadie más puede re-correr exactamente no es una matriz, es un recuerdo — y es la
# misma falla que el resto del proyecto existe para eliminar.
#
# Uso:  scripts/matrix-replica.sh [sandbox-dir]
#
# Regenera la réplica DESDE CERO con replica-ui.sh antes de medir, compila el binario del árbol
# actual, y exporta BATTEN_DB dentro del sandbox. La base real del usuario (~/.batten/batten.db)
# no se toca, y la última prueba lo verifica.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
SB="${1:-${TMPDIR:-/tmp}/batten-matrix-replica}"
R="$SB/replica-ui"
B="$SB/batten.exe"

mkdir -p "$SB"
echo "compilando el binario del árbol actual…"
(cd "$ROOT" && go build -o "$B" ./cmd/batten) || exit 1
echo "regenerando la réplica desde cero…"
bash "$HERE/replica-ui.sh" "$R" >/dev/null || exit 1

export BATTEN_DB="$R/.batten/batten.db"
cd "$R" || exit 1
"$B" init >/dev/null 2>&1

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); printf '  ✅ %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  ❌ %s\n' "$1"; }
check(){ if [ "$1" = "y" ]; then ok "$2"; else bad "$2"; fi }

# payload JSON sin dolores de cabeza con backslashes de Windows
CWDW=$(cygpath -w "$R" 2>/dev/null || echo "$R")
pay() { python -c "
import json,sys
d=json.loads(sys.argv[1]); d['cwd']=sys.argv[2]
print(json.dumps(d))" "$1" "$CWDW"; }
hook() { pay "$2" > "$SB/.p.json"; "$B" hook "$1" < "$SB/.p.json" 2>&1; }
has()  { grep -q "$2" <<<"$1" && echo y || echo n; }

echo "=== MATRIZ · réplica replica-ui (no es repo git, sin código, sin build files) ==="
echo

# ---- 1 · init -------------------------------------------------------------
echo "1 · init sobre 4 dominios sin código y 43 items"
check "$(has "$(cat batten.yaml)" 'unit:')" "batten.yaml existe y declara el unit"
check "$(has "$(cat batten.yaml)" "pattern: 'US-\\\\d{3}'")" "derivó el pattern del BACKLOG, no de las ramas"
check "$(has "$(grep -c 'AGENTS.md' batten.yaml)" '[1-9]')" "reconoció el proceso que el repo YA tiene"

# ---- 2 · doctor en una pasada --------------------------------------------
echo "2 · doctor: todo lo que sabe, de una vez"
D=$("$B" doctor 2>&1)
check "$(has "$D" 'declares no checks')" "avisa que el gate no verifica NADA"
check "$(has "$D" 'not a git repository')" "el ancla se declara y el repo no puede producirla"
check "$(has "$D" 'enforcement: REPORT')" "dice que los gates sólo avisan"
check "$(has "$D" 'query_before_read')" "cruza query_before_read con la realidad (ítem 15)"
N=$(grep -c '✓\|⚠\|●\|·' <<<"$D")
check "$([ "$N" -ge 8 ] && echo y || echo n)" "$N líneas de diagnóstico en UNA pasada (no una por corrida)"

# ---- pasar a enforce para las denegaciones -------------------------------
python - <<'PY'
import re
s=open('batten.yaml',encoding='utf-8').read()
s=s.replace('enforcement: report','enforcement: enforce')
s=s.replace('  tokens_per_run: 3000000','  tokens_per_run: 1')
s=s.replace('  on_exceed: warn','  on_exceed: block')
s=re.sub(r'  max_iterations: \d+', '  max_iterations: 2', s)
assert 'max_iterations: 2' in s, 'init no escribio max_iterations'
open('batten.yaml','w',encoding='utf-8',newline='').write(s)
PY

# ---- 3 · gate SIN checks + presupuesto excedido → DENY --------------------
echo "3 · gate sin checks + presupuesto excedido → DENY, con control positivo"
"$B" phase US-001 build >/dev/null 2>&1
"$B" phase US-001 close >/dev/null 2>&1
O=$(hook PreToolUse '{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git commit -m \"US-001: listo\""}}')
check "$(has "$O" '"permissionDecision":"deny"')" "el commit se deniega"
check "$(has "$O" 'batten.code:')" "la denegación lleva código tipado"
# CONTROL POSITIVO: el mismo payload con un comando que no es un commit debe pasar,
# y el log de decisiones debe probar que el hook CORRIÓ.
O2=$(hook PreToolUse '{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}')
check "$([ -z "$O2" ] && echo y || echo n)" "control: un comando que no es commit sale en silencio"
DEC=$(python -c "
import sqlite3,os
c=sqlite3.connect(os.environ['BATTEN_DB'])
print(*[r[0] for r in c.execute(\"select decision from events where hook='PreToolUse' order by event_id\")])")
check "$(has "$DEC" 'deny')" "control positivo: el log registra el deny…"
check "$(has "$DEC" 'allow')" "…y registra el allow, así que el hook SÍ corrió"

# ---- 4 · dos units en la misma fase --------------------------------------
echo "4 · dos units en la misma fase → ningún canvas se roba filas"
"$B" phase US-002 build >/dev/null 2>&1
N1=$(python -c "
import sqlite3,os
c=sqlite3.connect(os.environ['BATTEN_DB'])
q='select r.unit_id, count(n.node_id) from runs r left join nodes n on n.run_id=r.run_id where r.unit_id in (\"US-001\",\"US-002\") group by r.unit_id'
print(*[f'{u}:{k}' for u,k in c.execute(q)])")
check "$(has "$N1" 'US-001:')" "US-001 conserva sus nodos ($N1)"
check "$(has "$N1" 'US-002:')" "US-002 tiene los suyos"

# ---- 5 · commit cuyo mensaje nombra otro unit ----------------------------
echo "5 · commit cuyo mensaje nombra OTRO unit → DENY"
O=$(hook PreToolUse '{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git -c user.name=x commit -m \"US-042: otra cosa\""}}')
check "$(has "$O" 'US-042')" "nombra el unit del mensaje, no el de la sesión"
check "$(has "$O" 'permissionDecision\|warning')" "no pasa en silencio"

# ---- 6 · diff_from sin ancla (ítem 20) -----------------------------------
echo "6 · diff_from: anchor SIN ancla → lo dice (antes: silencio total)"
O=$("$B" phase US-001 verify 2>&1)
check "$(has "$O" 'no anchor')" "dice que no hay ancla"
check "$(has "$O" 'build')" "nombra la fase que debía grabarla"

# ---- 7 · scan-diff sin ancla (ítem 14) ----------------------------------
echo "7 · scan-diff sin ancla → 'no medible', nunca 'limpio'"
O=$("$B" scan-diff US-001 2>&1); E=$?
check "$([ $E -ne 0 ] && echo y || echo n)" "sale distinto de cero"
check "$(has "$O" 'measurable')" "distingue no-medible de limpio"

# ---- 8 · worktree sobre un repo que no es git (ítem 17) -----------------
echo "8 · worktree sobre un repo que NO es git → se niega y dice por qué"
O=$("$B" worktree US-001 2>&1); E=$?
check "$([ $E -ne 0 ] && echo y || echo n)" "se niega"
check "$(has "$O" 'not a git repository')" "dice exactamente qué falta"

# ---- 9 · las cuatro reglas desatendidas (ítem 16) -----------------------
echo "9 · modo desatendido: las cuatro reglas son denegaciones"
"$B" unattended US-002 >/dev/null 2>&1
O=$(hook PreToolUse '{"session_id":"s9","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf frontend"}}')
check "$(has "$O" 'unattended_delete')" "regla 1: rm denegado"
O=$("$B" override US-002 --reason "seguro que está bien" 2>&1)
check "$(has "$O" 'unattended_override')" "regla 3: override rechazado"
"$B" iterate US-002 >/dev/null 2>&1; "$B" iterate US-002 >/dev/null 2>&1
O=$("$B" iterate US-002 2>&1)
check "$(has "$O" 'iteration_ceiling')" "regla 2: el techo de iteraciones se impone"
O=$(hook PreToolUse '{"session_id":"s9","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git commit -m \"US-002: x\""}}')
check "$(has "$O" 'unattended_commit')" "regla 4: commit denegado"
# CONTROL: apagado el modo, el rm vuelve a pasar
"$B" unattended US-002 --off >/dev/null 2>&1
O=$(hook PreToolUse '{"session_id":"s9","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf frontend"}}')
check "$([ -z "$O" ] && echo y || echo n)" "control: con el modo apagado el rm pasa"

# ---- 10 · el bypass por Bash (ítem 18) ---------------------------------
echo "10 · sed -i sobre el archivo de otro agente → aviso (antes: silencio)"
hook SubagentStart '{"session_id":"s1","agent_id":"ag-fe","agent_type":"frontend","hook_event_name":"SubagentStart"}' >/dev/null
"$B" claim ag-fe frontend/AGENTS.md >/dev/null 2>&1
O=$(hook PreToolUse '{"session_id":"s1","agent_id":"ag-be","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"sed -i s/a/b/ frontend/AGENTS.md"}}')
check "$(has "$O" 'frontend/AGENTS.md')" "nombra el archivo"
check "$(has "$O" 'sed -i')" "nombra cómo se iba a escribir"
check "$(has "$O" 'warning')" "es AVISO, no denegación (un ciclo midiendo)"

# ---- 11 · la orientación llega al agente (ítem 15) ---------------------
echo "11 · la cadena de memorias llega en SessionStart"
O=$(hook SessionStart '{"session_id":"s1","hook_event_name":"SessionStart","source":"startup"}')
check "$(has "$O" 'orient BEFORE you read')" "la instrucción se inyecta"
check "$(has "$O" 'say so')" "exige declarar si ninguna memoria respondió"

# ---- 11b · criterios como dato (ítem 21) --------------------------------
echo "11b · los criterios del backlog se vuelven filas, y el PR los cuenta"
# US-003 está en docs/backlog.md con dos criterios; phase los siembra.
"$B" phase US-003 build >/dev/null 2>&1
O=$("$B" status 2>&1)
check "$(has "$O" 'US-003')" "batten status muestra el unit del backlog"
check "$(has "$O" 'AC 0/2 covered')" "y sus criterios sembrados, sin cubrir todavía"
check "$(has "$O" 'not started')" "los units que nadie arrancó también aparecen"
printf '{"check_id":"qa","result":"ok","why":"revisado","evidence":["AC-1: criterio A verificado a mano"]}' > "$SB/.v21.json"
"$B" verdict --unit US-003 --file "$SB/.v21.json" >/dev/null 2>&1
O=$("$B" status 2>&1)
check "$(has "$O" 'AC 1/2 covered')" "la evidencia que cita AC-1 lo cubre; AC-2 sigue abierto"
O=$("$B" pr US-003 2>&1)
check "$(has "$O" '1 of 2 covered')" "el PR dice cuántos criterios cubrió"
check "$(has "$O" 'not covered')" "y nombra el que NO — un tablero que solo muestra lo verde adula"

# ---- 12 · degradación sin git, sin checks, sin build files -------------
echo "12 · el repo entero degradó sin reventar"
check "$([ -f .batten/batten.db ] && echo y || echo n)" "la base quedó DENTRO del sandbox"
# NO por mtime: el plugin instalado corre sobre el repo del usuario en cada llamada de
# herramienta, así que su mtime cambia todo el tiempo por operación normal y legítima. Lo que hay
# que probar es que NINGÚN proyecto de sandbox aparezca ahí.
CONTAM=$(python -c "
import sqlite3,os
p=os.path.expanduser('~/.batten/batten.db')
if not os.path.exists(p): print('none'); raise SystemExit
c=sqlite3.connect('file:'+p+'?mode=ro',uri=True)
projs={r[0] for r in c.execute('select distinct project from runs')}
bad=projs & {'replica-ui','sb14','sb16','sb17','sb20','demo','taskly'}
print(','.join(sorted(bad)) if bad else 'none')")
check "$(has "$CONTAM" 'none')" "la base REAL del usuario no tiene NINGÚN proyecto de sandbox"

echo
echo "=== RÉPLICA: $PASS pasan, $FAIL fallan ==="
[ "$FAIL" -eq 0 ]
