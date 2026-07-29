#!/usr/bin/env bash
# La matriz de `batten demo`, re-corrida sobre el bloque 3.
# demo construye su propio repo git, corre el flujo entero y lo borra: es a la vez el recorrido de
# adopción y el único test de integración end-to-end del proyecto.
# Uso:  scripts/matrix-demo.sh [sandbox-dir]
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
SB="${1:-${TMPDIR:-/tmp}/batten-matrix-demo}"
D="$SB/demo-run"
B="$SB/batten.exe"

rm -rf "$D"; mkdir -p "$D"
echo "compilando el binario del árbol actual…"
(cd "$ROOT" && go build -o "$B" ./cmd/batten) || exit 1

export BATTEN_DB="$D/demo.db"
cd "$D" || exit 1

"$B" demo > "$D/raw.txt" 2>&1
DEMO_EXIT=$?
# demo pinta en color; la matriz lee texto plano.
sed 's/\x1b\[[0-9;]*m//g' "$D/raw.txt" > "$D/out.txt"
O=$(cat "$D/out.txt")

PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); printf '  ✅ %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); printf '  ❌ %s\n' "$1"; }
want(){ if grep -qF -- "$1" "$D/out.txt"; then ok "$2"; else bad "$2  [falta: $1]"; fi }
deny(){ if grep -q -- "$1" "$D/out.txt"; then ok "$2"; else bad "$2  [falta: $1]"; fi }

echo "=== MATRIZ · batten demo (repo git propio, código con un bug real) ==="
echo

echo "0 · el demo corre entero"
if [ "$DEMO_EXIT" -eq 0 ]; then ok "sale 0"; else bad "sale $DEMO_EXIT"; fi
want "The sandbox is gone." "borra su propio sandbox"
want "your database were never touched" "dice que no tocó nada del usuario"

echo "1 · init lee el repo y propone un spec"
want "batten init" "el paso existe"
want "domains detected from their AGENTS.md" "detecta los dominios del proceso que el repo YA tiene"
want "derived from the backlog's headings, not from a branch name" "deriva el unit del backlog"
want "lifted verbatim from the Makefile" "los checks salen de los build files, no inventados"

echo "2 · commit antes de abrir un run → avisa que no gobierna nada"
want "not governing" "el silencio del primer commit está cerrado"

echo "3 · phase abre el run y ancla"
want "batten phase" "el paso existe"

echo "4 · commit sin veredicto → DENY"
deny "no_verdict\|has no verdict" "denegado por falta de veredicto"

echo "5 · el revisor aprueba y SIGUE denegado (dos productores)"
deny "checks_not_run\|checks must be RUN\|no batten-verified" "un veredicto de agente no alcanza"

echo "6 · dos agentes en fan-out → el segundo no escribe el archivo del primero"
deny "write_set\|write-set collision" "la colisión se deniega"
want "fix the plan" "y NO ofrece una salida: el plan está mal"

echo "7 · check corre de verdad y uno falla"
want "batten check" "el paso existe"
deny "FAIL\|exit 1\|did not pass" "un check que falla se reporta como fallado"

echo "8 · arreglado el bug, los checks pasan"
deny "PASS\|all gate checks passed" "el check pasa cuando el código está bien"

echo "9 · ahora el commit se permite"
want "allowed" "el commit pasa cuando el gate está satisfecho"

echo "10 · algo edita el árbol DESPUÉS de que los checks pasaron"
deny "stale_target" "el target estancado se detecta"
want "batten check" "y dice cómo re-verificar"

echo "11 · report: qué pasó y qué frenó batten"
want "what batten stopped" "el bloque de impacto existe"
want "commit(s) denied" "cuenta los commits denegados"
deny "counting since" "dice desde cuándo cuenta (no un total histórico falso)"
want "NOT MEASURED" "el uso sin medir NO se reporta como 0"

echo "12 · el sobre tipado viaja en las denegaciones"
want "batten.code:" "código estable"
want "batten.retry:" "si reintentar sirve"
want "batten.fix:" "el comando exacto, donde hay salida legítima"

echo
echo "=== DEMO: $PASS pasan, $FAIL fallan ==="
[ "$FAIL" -eq 0 ]
