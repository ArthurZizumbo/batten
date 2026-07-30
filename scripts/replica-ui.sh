#!/usr/bin/env bash
# replica-ui.sh — rebuild the sandbox replica of proyecto_ui.
#
# WHY THIS IS A COMMITTED SCRIPT AND NOT A THROWAWAY
#
# batten's test suite and its synthetic sandboxes all describe a repo that has code, build files
# and git history. proyecto_ui — the only project batten was ever exercised against that it did
# not itself write — has none of those:
#
#   |               | taskly (the synthetic sandbox) | proyecto_ui              |
#   |---------------|--------------------------------|--------------------------|
#   | git repo      | yes                            | NO                       |
#   | code          | Go with tests                  | NONE: 4 AGENTS.md, no src |
#   | backlog       | 5 items                        | 40+ US-0NN               |
#   | build files   | Makefile                       | none -> gates.checks EMPTY|
#
# That last row is the one that bites. A repo with no build files lands in the gate's
# no-checks branch, which is exactly where the `on_exceed: block` regression lived. Eight fixes
# were validated against the shapes above the line and never against the shape below it.
#
# The first time this replica was built it was built in a temp directory and thrown away, so the
# whole thing had to be reconstructed from a description months later. It is a script now so
# that cannot happen again.
#
# Usage:  scripts/replica-ui.sh [target-dir]
#
# ALWAYS export BATTEN_DB into the sandbox before every batten command you run against it.
# Without it dbPath() falls back to the user's REAL database (~/.batten/batten.db) and the
# test contaminates the vault it was supposed to leave alone:
#
#   export BATTEN_DB="$TARGET/.batten/batten.db"
#
set -euo pipefail

TARGET="${1:-${TMPDIR:-/tmp}/batten-replica-ui}"

rm -rf "$TARGET"
mkdir -p "$TARGET/docs" "$TARGET/frontend" "$TARGET/backend" "$TARGET/design-system" "$TARGET/infra"

cat > "$TARGET/AGENTS.md" <<'EOF'
# Reglas del proyecto

- Ningun componente se acepta sin su historia de Storybook.
- Los tokens de diseno no se hardcodean: salen de design-system.
- Toda llamada a la API pasa por el cliente generado, nunca fetch directo.
EOF

for d in frontend backend design-system infra; do
	cat > "$TARGET/$d/AGENTS.md" <<EOF
# $d

## Invariantes
- Nada en $d escribe fuera de $d.
- Los cambios de $d se revisan contra el backlog antes de cerrarse.
EOF
done

# 43 work items. init needs >=3 \`### US-0NN\` headings before it will call US a convention;
# below that it deliberately falls back to TASK-\d+, so the count is load-bearing.
{
	echo "# Backlog"
	echo
	for i in $(seq -w 1 43); do
		printf '### US-0%s — historia %s\n\n' "$i" "$i"
		printf 'Como usuario quiero la funcionalidad %s para poder trabajar.\n\n' "$i"
		printf '**Criterios de aceptacion**\n- criterio A de US-0%s\n- criterio B de US-0%s\n\n' "$i" "$i"
	done
} > "$TARGET/docs/backlog.md"

cat > "$TARGET/docs/arquitectura.md" <<'EOF'
# Arquitectura

Cuatro dominios: frontend, backend, design-system, infra.
Todavia no hay codigo: el proyecto esta planeado, no construido.
EOF

# Deliberately absent, each for a reason the matrix depends on:
#   - no `git init`      -> phases[].anchor: git_sha has nothing to anchor to (matrix test 8)
#   - no Makefile/pkg.json/go.mod -> gates.checks stays EMPTY (the branch the regression lived in)
#   - no source files    -> domains are declared by AGENTS.md alone

echo "replica written to: $TARGET"
echo
echo "next:"
echo "  export BATTEN_DB=\"$TARGET/.batten/batten.db\"   # BEFORE every batten command"
echo "  (cd \"$TARGET\" && batten init)"
echo
echo "the 8-test matrix this exists for is in docs/field-test/REPLICA-UI.md"
