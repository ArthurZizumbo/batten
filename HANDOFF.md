# HANDOFF — batten v2 (mejoras del PLAN.md)

> Estado vivo de la ejecución del PLAN.md. Si una sesión se corta, retomar desde "Próximo paso".
> Última actualización: 2026-07-14, inicio de la ejecución v2.

## Dónde retomar
**Código terminado**: MVP + v2 (E0/P1/P2/P2.5/P3/P4) + hardening (fix 0bee413) + v3
(R1/R2/R3/R4/M1). Falta SOLO E0 (Arthur instala el plugin y corre `docs/E0-DOGFOOD.md`).

Los 2 resultados de E0 que pueden cambiar diseño: el `.exe` en hooks de Windows, y si `agent_id`
llega en PreToolUse de subagente (si no, el guard YA cae a advisory automáticamente — corregido
en 0bee413, ya no bloquea el fan-out).

**Antes del primer release**: fijar el repo real (`arthu/batten` es placeholder) en
`scripts/bootstrap.sh` y `.goreleaser.yaml`, y decidir el module path definitivo.

## v3 (robustez pre-pruebas) — HECHO
- R1 ciclo de vida: `batten close`, auto-close al commitear, doctor avisa runs stale >48h.
- R2 `batten check`: corre los checks del gate DE VERDAD, graba veredicto source='batten'; el
  commit gate exige ese veredicto (mata "escribí que pasa sin correrlo").
- R3 panic fence en cmdHook. R4 case-fold de paths en Windows.
- M1 ruteo de modelos: `models.tiers`/`phases` + `domains[].model`; `batten show` marca desviación
  declarado-vs-real; `batten measure` desglosa por modelo. Verificado todo.

## Fases y estado
- [x] E0 — artefactos de dogfooding LISTOS. Arthur debe correr `docs/E0-DOGFOOD.md` (5 pasos) e
      pegar resultados. `batten hook-debug --tap/--show` captura el `agent_id` real. batten.yaml
      propio creado (dogfood, enforcement: report). Build scripts en scripts/.
      **Pendiente de Arthur**: los 5 resultados del spike (solo #1 .exe y #2 agent_id cambian diseño).
- [x] P1 — `batten init` real (internal/scan) + `enforcement: report`. Verificado en repo TS.
      Commit incluido. init deriva unidad/dominios/checks reales; report mode advierte en vez de bloquear.
- [x] P2 — vault AUTO (internal/export, dispara en Stop hook + tras veredicto — verificado),
      engram reinyectado en commands, graphify staleness en doctor, headroom measure real
      (migración user_version + `batten measure`). Commit incluido.
- [x] P2.5 — multi-sesión. binding sesión↔run (RunBySession + PostToolUse hook), write-set guard
      ENTRE runs abiertos, ambigüedad visible en sessionStart. Verificado: US-034/sessA vs
      US-051/sessB; sessB pisando archivo de US-034 → DENY. Commit incluido.
- [x] P3 — distribución. release.yml (GoReleaser en tag v*), bootstrap.sh (SessionStart descarga
      el binario a CLAUDE_PLUGIN_DATA), build-plugin scripts, docs/INSTALL.md. Commit incluido.
- [x] P4 — verificación end-to-end. Suite verde; report→WARN / enforce→DENY; migración user_version
      0→1 en sitio; measure dice "insufficient" con <3 runs. Resultados en docs/VERIFICATION.md.

## Contexto imprescindible
- Repo: `c:/Users/arthu/Proyectos/Public/LoopWorkFlow`, module `github.com/arthu/batten`, rama `feat/batten-mvp`.
- Binario: `go build -o plugin/claude-code/bin/batten.exe ./cmd/batten`. DB por `BATTEN_DB` env o `${CLAUDE_PLUGIN_DATA}/batten.db`.
- MVP ya commiteado (`1c1be26`): gates, contabilidad, canvas, neutralidad — todo verificado en sandbox.
- Paquetes: internal/{spec,store,hooks,canvas,usage,statusline,mcp,vault,discovery,tui}.
- Principios NO negociables: (1) nunca inventar un número; (2) degradar, no romper; (3) fallar-abierto solo con aviso; (4) override queda en el log.

## Notas de decisiones tomadas durante la ejecución
(se llenan conforme avanzo)

## Verificación de cada fase
(se llena conforme avanzo)
